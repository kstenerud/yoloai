#!/usr/bin/env bash
# ABOUTME: Reproduces the import-direction check from the 2026-05 audit.
# ABOUTME: Lists every internal cross-package import and flags inversions
# ABOUTME: (notably runtime/* importing config/, which F5 called out).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

MOD="github.com/kstenerud/yoloai"

echo "## Cross-package imports of internal modules"
echo
echo "Format: importer-package -> imported-package  (count)"
echo

# Find every import of an in-module package and tally (importer, imported) pairs.
grep -rE "^\s*\"${MOD}/" --include="*.go" --exclude="*_test.go" . \
  | awk -v mod="$MOD" '
      {
        # Path before the colon is "./pkg/file.go" — derive importer pkg
        split($0, parts, ":")
        path = parts[1]
        sub(/^\.\//, "", path)
        n = split(path, segs, "/")
        importer = segs[1]
        for (i = 2; i < n; i++) importer = importer "/" segs[i]

        # Path inside the quotes is the import target — derive imported pkg
        match($0, /"[^"]+"/)
        imp = substr($0, RSTART+1, RLENGTH-2)
        sub(mod"/", "", imp)

        if (importer != imp) tally[importer "\t-> " imp]++
      }
      END { for (k in tally) printf "%4d  %s\n", tally[k], k }
    ' \
  | sort -k3

echo
echo "## Direction inversions (F5)"
echo
echo "F5's stated scope was runtime/* depending on config/ for *typed error"
echo "constructors* (NewDependencyError, NewPermissionError, etc.). Those"
echo "moved to internal/yoerrors in W7. Remaining runtime/* -> config edges"
echo "are for path/constant helpers (SandboxesDir, BackendDirName,"
echo "EncodePath, CacheDir, HomeDir, ...) — out of F5's scope."
echo
echo "Remaining runtime/* -> config edges:"
grep -rn "\"${MOD}/config" runtime/ --include="*.go" --exclude="*_test.go" || true
echo
echo "Verify they are only path/constant helpers (no error constructors):"
grep -rE "config\.New[A-Z][A-Za-z]*Error" runtime/ --include="*.go" --exclude="*_test.go" \
  | head -5 || echo "(none — F5 closed)"
