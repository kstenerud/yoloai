// ABOUTME: The gate that stops the threaded writer coming back. Library code
// ABOUTME: reports through feedback sinks; an io.Writer parameter is how the
// ABOUTME: old mechanism reappears, so every one has to be declared here with
// ABOUTME: a reason that is about bytes rather than about telling somebody.

package yoloai_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writerFencePackages are the library trees this fence covers: everything that
// reports to a caller but does not own the terminal. internal/cli and cmd/ are
// excluded because rendering is their job.
var writerFencePackages = []string{
	"runtime", "store", "copyflow", "feedback",
	"internal/orchestrator", "internal/envsetup", "internal/broker",
	"internal/config", "internal/agent", "internal/netpolicy", "internal/workspace",
	"internal/git", "internal/credential",
}

// licensedWriters are the io.Writer parameters that survive, each because it
// carries *bytes* rather than because it tells somebody something.
//
// The distinction is the whole rule, and it is what the D145 amendment of
// 2026-08-19 got wrong the first time: "carries a stream we do not own" was
// used to justify writers on Setup and BuildProfileImage, whose writer
// parameters were in fact receiving sentences the library composed. A writer
// here has to be moving opaque bytes to or from something else — a terminal, a
// subprocess, a protocol peer, a document under construction.
//
// Keyed "<package>.<func>" or "<package>.<Type>.<method>". Adding an entry is a
// deliberate act: say what the bytes are, and if the answer is "a message for
// the user", it is a notice or a progress record instead.
var licensedWriters = map[string]string{
	// Terminal I/O. A live bidirectional PTY bridge, byte-exact and
	// latency-sensitive; records would be absurd.
	"runtime.IOStreams.Out":         "process stdout, bridged to a real terminal",
	"runtime.IOStreams.Err":         "process stderr, bridged to a real terminal",
	"runtime.StdioExecer.StdioExec": "the guest process's stdio, by definition",
	"docker.Runtime.StdioExec":      "the guest process's stdio, by definition",
	"orchestrator.Engine.StdioExec": "the guest process's stdio, by definition",
	"ptybridge.crStripper.w":        "the PTY's byte stream, mid-filter",

	// Protocol peers.
	"broker.RunSidecar": "the sidecar handshake stream, not prose",

	// Byte-stream utilities: these are io.Writer *implementations* and adapters,
	// which is the shape the rest of the tree is being kept away from precisely
	// so that it stays confined here.
	"feedback.WriterSink":       "the adapter that renders records to a caller's byte stream",
	"feedback.ProgressToWriter": "the adapter that renders progress to a caller's byte stream",
}

// TestArch_LibraryTakesNoFeedbackWriter fails when a library function grows an
// io.Writer parameter that is not on the licensed list.
//
// The claim it pins: **library code reports through feedback sinks, never by
// writing formatted text to a threaded writer.** Without a gate this decays
// quietly — a writer parameter looks disciplined, since it is explicit rather
// than global, while hardcoding the one assumption a non-CLI consumer cannot
// satisfy: that a human is watching text, in order, now. Every conversion this
// release undid was of exactly that shape.
//
// A test rather than a forbidigo rule because forbidigo matches call names, not
// argument types: it cannot tell `Fprintf(w, …)` to a threaded writer from
// `Fprintf(&builder, …)` building a string. Banning `fmt.Fprint*` outright
// would express it, at the price of rewriting ~60 legitimate string-building
// sites and outlawing idiomatic Go. This states the actual invariant instead.
func TestArch_LibraryTakesNoFeedbackWriter(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	seen := map[string]bool{}
	checked := 0
	for _, pkgDir := range writerFencePackages {
		err := filepath.WalkDir(filepath.Join(root, pkgDir), func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			checked++
			offenders = append(offenders, writerParamsIn(t, path, root, seen)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v — a fence that cannot read its corpus is checking nothing", pkgDir, err)
		}
	}

	if checked == 0 {
		t.Fatal("the fence scanned no files — it is checking nothing, which is the DF94 failure mode")
	}
	t.Logf("writer fence scope: %d library files, %d licensed writers", checked, len(licensedWriters))

	// An allowlist entry that matches nothing is a claim about code that no
	// longer exists, and it is how a fence quietly stops fencing: the next
	// writer to appear under a stale name is licensed by accident.
	for key := range licensedWriters {
		if !seen[key] {
			t.Errorf("licensedWriters has %q, which matches no declaration — delete it, or the "+
				"next writer to take that name is licensed by accident", key)
		}
	}

	if len(offenders) == 0 {
		return
	}
	sort.Strings(offenders)
	t.Errorf("library code must report through feedback.Sink / feedback.ProgressSink, not an "+
		"io.Writer parameter (D145). If these carry opaque bytes — a terminal, a subprocess, a "+
		"protocol peer — add them to licensedWriters with a reason that is about bytes. If they "+
		"carry a message for a human, they are a Notice or a Progress:\n  %s",
		strings.Join(offenders, "\n  "))
}

// writerParamsIn returns the unlicensed io.Writer parameters declared in one
// file, as "<relpath>:<line> <key>", recording every licensed key it matched in
// seen so the allowlist can be checked for staleness.
func writerParamsIn(t *testing.T, path, root string, seen map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	rel, _ := filepath.Rel(root, path)

	var found []string
	report := func(pos token.Pos, key string) {
		if _, ok := licensedWriters[key]; ok {
			seen[key] = true
			return
		}
		found = append(found, fmt.Sprintf("%s:%d %s", rel, fset.Position(pos).Line, key))
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			inspectFuncDecl(decl, file, report)
		case *ast.InterfaceType:
			inspectInterface(decl, file, report)
		case *ast.StructType:
			inspectStruct(decl, file, report)
		}
		return true
	})
	return found
}

// hasWriterField reports whether any field in the list is an io.Writer.
func hasWriterField(fields []*ast.Field) bool {
	for _, f := range fields {
		if isWriterExpr(f.Type) {
			return true
		}
	}
	return false
}

// isWriterExpr reports whether an expression names io.Writer.
func isWriterExpr(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Writer" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "io"
}

// receiverName returns the bare type name of a method receiver.
func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	}
	return "?"
}

// enclosingTypeName finds the named type declaration containing pos.
func enclosingTypeName(file *ast.File, pos token.Pos) string {
	name := "?"
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if ok && spec.Pos() <= pos && pos <= spec.End() {
			name = spec.Name.Name
		}
		return true
	})
	return name
}

// inspectFuncDecl reports a function or method whose parameters include a writer.
func inspectFuncDecl(decl *ast.FuncDecl, file *ast.File, report func(token.Pos, string)) {
	if decl.Type.Params == nil || !hasWriterField(decl.Type.Params.List) {
		return
	}
	key := file.Name.Name + "." + decl.Name.Name
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		key = file.Name.Name + "." + receiverName(decl.Recv.List[0].Type) + "." + decl.Name.Name
	}
	report(decl.Pos(), key)
}

// inspectInterface reports interface methods that take a writer.
//
// These matter most: a writer on an interface obliges every implementation to
// accept one, which is how the old mechanism spread across five backends.
func inspectInterface(decl *ast.InterfaceType, file *ast.File, report func(token.Pos, string)) {
	for _, m := range decl.Methods.List {
		fn, ok := m.Type.(*ast.FuncType)
		if !ok || len(m.Names) == 0 || fn.Params == nil || !hasWriterField(fn.Params.List) {
			continue
		}
		report(m.Pos(), file.Name.Name+"."+enclosingTypeName(file, m.Pos())+"."+m.Names[0].Name)
	}
}

// inspectStruct reports struct fields typed io.Writer — the state.State.Output
// shape, which is a threaded writer that skipped the parameter list.
func inspectStruct(decl *ast.StructType, file *ast.File, report func(token.Pos, string)) {
	for _, f := range decl.Fields.List {
		if !isWriterExpr(f.Type) || len(f.Names) == 0 {
			continue
		}
		report(f.Pos(), file.Name.Name+"."+enclosingTypeName(file, f.Pos())+"."+f.Names[0].Name)
	}
}
