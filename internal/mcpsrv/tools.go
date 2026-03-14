package mcpsrv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) registerTools() {
	// Help
	s.srv.AddTool(yoloaiHelpTool(), handleYoloaiHelp)

	// Lifecycle
	s.srv.AddTool(sandboxCreateTool(), s.handleSandboxCreate)
	s.srv.AddTool(sandboxStatusTool(), s.handleSandboxStatus)
	s.srv.AddTool(sandboxListTool(), s.handleSandboxList)
	s.srv.AddTool(sandboxDestroyTool(), s.handleSandboxDestroy)

	// Observation
	s.srv.AddTool(sandboxDiffTool(), s.handleSandboxDiff)
	s.srv.AddTool(sandboxDiffFileTool(), s.handleSandboxDiffFile)
	s.srv.AddTool(sandboxLogTool(), s.handleSandboxLog)

	// Refinement
	s.srv.AddTool(sandboxInputTool(), s.handleSandboxInput)
	s.srv.AddTool(sandboxResetTool(), s.handleSandboxReset)

	// Files (Q&A channel)
	s.srv.AddTool(sandboxFilesListTool(), s.handleSandboxFilesList)
	s.srv.AddTool(sandboxFilesReadTool(), s.handleSandboxFilesRead)
	s.srv.AddTool(sandboxFilesWriteTool(), s.handleSandboxFilesWrite)
}

// ── Tool definitions ──────────────────────────────────────────────────────────

func sandboxCreateTool() mcp.Tool {
	return mcp.NewTool("sandbox_create",
		mcp.WithDescription("Create a new sandbox and start the agent. Returns immediately — poll sandbox_status every 5s until agent_status is done or failed."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Unique sandbox name (alphanumeric, hyphens, underscores)")),
		mcp.WithString("workdir", mcp.Required(), mcp.Description("Absolute path to the host working directory")),
		mcp.WithString("prompt", mcp.Description("Task description for the agent")),
		mcp.WithString("agent", mcp.Description("Agent to use (claude, gemini, codex, aider). Default: from config.")),
		mcp.WithString("model", mcp.Description("Model override. Default: agent's default.")),
		mcp.WithString("profile", mcp.Description("Profile name for environment customization.")),
	)
}

func sandboxStatusTool() mcp.Tool {
	return mcp.NewTool("sandbox_status",
		mcp.WithDescription("Get sandbox status. Poll this after sandbox_create (every 5s). agent_status: active=working, idle=waiting at prompt, done=finished, failed=error. Check sandbox_files_read for question.json when waiting_for_input."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
	)
}

func sandboxListTool() mcp.Tool {
	return mcp.NewTool("sandbox_list",
		mcp.WithDescription("List all sandboxes with their current status."),
	)
}

func sandboxDestroyTool() mcp.Tool {
	return mcp.NewTool("sandbox_destroy",
		mcp.WithDescription("Destroy a sandbox and remove its container. Use after applying changes."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
		mcp.WithBoolean("force", mcp.Description("Force destroy even if there are unapplied changes")),
	)
}

func sandboxDiffTool() mcp.Tool {
	return mcp.NewTool("sandbox_diff",
		mcp.WithDescription("Show diff of all changes made in the sandbox. Call with stat=true first for a cheap summary. Use sandbox_diff_file for specific files when the full diff is too large. Note: overlay-mode directories require a running container."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
		mcp.WithBoolean("stat", mcp.Description("Return stat summary only (default false)")),
	)
}

func sandboxDiffFileTool() mcp.Tool {
	return mcp.NewTool("sandbox_diff_file",
		mcp.WithDescription("Show diff for a specific file path in the sandbox. More efficient than fetching the full diff when reviewing individual files."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
		mcp.WithString("path", mcp.Required(), mcp.Description("File path relative to the sandbox working directory")),
	)
}

func sandboxLogTool() mcp.Tool {
	return mcp.NewTool("sandbox_log",
		mcp.WithDescription("Read the sandbox agent log. Returns the last N lines of the agent's output log."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
		mcp.WithNumber("lines", mcp.Description("Number of lines to return (default 100)")),
	)
}

func sandboxInputTool() mcp.Tool {
	return mcp.NewTool("sandbox_input",
		mcp.WithDescription("Send text input to the sandbox agent's terminal via tmux. Check sandbox_status first: if agent_status is 'idle', this continues the conversation. If 'active', this interrupts the current task — use carefully."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
		mcp.WithString("text", mcp.Required(), mcp.Description("Text to send to the agent")),
	)
}

func sandboxResetTool() mcp.Tool {
	return mcp.NewTool("sandbox_reset",
		mcp.WithDescription("Reset the sandbox to a clean state. Use when the agent has gone off track and the conversation context is poisoned. Starts fresh. Poll sandbox_status after calling."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
		mcp.WithString("prompt", mcp.Description("Revised prompt for the new session. If omitted, re-uses the original prompt.")),
	)
}

func sandboxFilesListTool() mcp.Tool {
	return mcp.NewTool("sandbox_files_list",
		mcp.WithDescription("List files in the sandbox file exchange directory (/yoloai/files/). Check here for question.json when the agent needs input."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
	)
}

func sandboxFilesReadTool() mcp.Tool {
	return mcp.NewTool("sandbox_files_read",
		mcp.WithDescription("Read a file from the sandbox file exchange directory. Use to read question.json when the agent is waiting for input."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
		mcp.WithString("filename", mcp.Required(), mcp.Description("Filename (no path separators)")),
	)
}

func sandboxFilesWriteTool() mcp.Tool {
	return mcp.NewTool("sandbox_files_write",
		mcp.WithDescription("Write a file to the sandbox file exchange directory. Write answer.json here to respond to a question.json the agent created. Format: {\"answer\": \"your answer here\"}. filename must be a plain filename with no path separators."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sandbox name")),
		mcp.WithString("filename", mcp.Required(), mcp.Description("Filename (no path separators)")),
		mcp.WithString("content", mcp.Required(), mcp.Description("File content to write")),
	)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *Server) handleSandboxCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	workdir := req.GetString("workdir", "")
	prompt := req.GetString("prompt", "")
	agent := req.GetString("agent", "")
	model := req.GetString("model", "")
	profile := req.GetString("profile", "")

	if name == "" {
		return textResult(errorf("name is required")), nil
	}
	if workdir == "" {
		return textResult(errorf("workdir is required")), nil
	}

	if err := s.mgr.EnsureSetup(ctx); err != nil {
		return textResult(errorf("setup: %v", err)), nil
	}

	opts := sandbox.CreateOptions{
		Name: name,
		Workdir: sandbox.DirSpec{
			Path: workdir,
			Mode: sandbox.DirModeCopy,
		},
		Agent:   agent,
		Model:   model,
		Profile: profile,
		Prompt:  prompt,
		Yes:     true,
	}

	if _, err := s.mgr.Create(ctx, opts); err != nil {
		return textResult(errorf("create sandbox: %v", err)), nil
	}

	return textResult(fmt.Sprintf("Sandbox %q created. Poll sandbox_status every 5s.", name)), nil
}

func (s *Server) handleSandboxStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	if name == "" {
		return textResult(errorf("name is required")), nil
	}

	info, err := s.mgr.Inspect(ctx, name)
	if err != nil {
		return textResult(errorf("inspect sandbox %q: %v", name, err)), nil
	}

	// Format as JSON for easy parsing by the outer agent
	out, _ := json.MarshalIndent(map[string]any{
		"name":         info.Meta.Name,
		"status":       string(info.Status),
		"agent_status": string(info.AgentStatus),
		"agent":        info.Meta.Agent,
		"model":        info.Meta.Model,
		"has_changes":  info.HasChanges,
		"disk_usage":   info.DiskUsage,
		"created":      info.Meta.CreatedAt,
	}, "", "  ")

	return textResult(string(out)), nil
}

func (s *Server) handleSandboxList(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	infos, err := s.mgr.List(ctx)
	if err != nil {
		return textResult(errorf("list sandboxes: %v", err)), nil
	}

	if len(infos) == 0 {
		return textResult("No sandboxes found."), nil
	}

	type entry struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		AgentStatus string `json:"agent_status,omitempty"`
		Agent       string `json:"agent"`
		HasChanges  string `json:"has_changes"`
		DiskUsage   string `json:"disk_usage"`
	}

	entries := make([]entry, 0, len(infos))
	for _, info := range infos {
		entries = append(entries, entry{
			Name:        info.Meta.Name,
			Status:      string(info.Status),
			AgentStatus: string(info.AgentStatus),
			Agent:       info.Meta.Agent,
			HasChanges:  info.HasChanges,
			DiskUsage:   info.DiskUsage,
		})
	}

	out, _ := json.MarshalIndent(entries, "", "  ")
	return textResult(string(out)), nil
}

func (s *Server) handleSandboxDestroy(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	force := req.GetBool("force", false)

	if name == "" {
		return textResult(errorf("name is required")), nil
	}

	if !force {
		needs, reason := s.mgr.NeedsConfirmation(ctx, name)
		if needs {
			return textResult(errorf("sandbox %q has unapplied changes (%s). Use force=true to destroy anyway.", name, reason)), nil
		}
	}

	if err := s.mgr.Destroy(ctx, name); err != nil {
		return textResult(errorf("destroy sandbox %q: %v", name, err)), nil
	}

	return textResult(fmt.Sprintf("Sandbox %q destroyed.", name)), nil
}

func (s *Server) handleSandboxDiff(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	stat := req.GetBool("stat", false)

	if name == "" {
		return textResult(errorf("name is required")), nil
	}

	results, err := sandbox.GenerateMultiDiff(name, stat)
	if err != nil {
		return textResult(errorf("diff sandbox %q: %v", name, err)), nil
	}

	if len(results) == 0 {
		return textResult("[ERROR] no changes to diff"), nil
	}

	var parts []string
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("--- %s ---\n%s", r.WorkDir, r.Output))
	}

	return textResult(strings.Join(parts, "\n\n")), nil
}

func (s *Server) handleSandboxDiffFile(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	path := req.GetString("path", "")

	if name == "" {
		return textResult(errorf("name is required")), nil
	}
	if path == "" {
		return textResult(errorf("path is required")), nil
	}

	result, err := sandbox.GenerateDiff(sandbox.DiffOptions{
		Name:  name,
		Paths: []string{path},
	})
	if err != nil {
		return textResult(errorf("diff file %q in sandbox %q: %v", path, name, err)), nil
	}

	if result.Empty || result.Output == "" {
		return textResult(fmt.Sprintf("No changes in %s", path)), nil
	}

	return textResult(result.Output), nil
}

func (s *Server) handleSandboxLog(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	linesFloat := req.GetFloat("lines", 100)

	if name == "" {
		return textResult(errorf("name is required")), nil
	}

	lines := int(linesFloat)
	if lines <= 0 {
		lines = 100
	}

	logPath := sandbox.LogFilePath(name)
	output, err := tailFile(logPath, lines)
	if err != nil {
		return textResult(errorf("read log for sandbox %q: %v", name, err)), nil
	}

	if output == "" {
		return textResult("(no log output)"), nil
	}

	return textResult(output), nil
}

func (s *Server) handleSandboxInput(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	text := req.GetString("text", "")

	if name == "" {
		return textResult(errorf("name is required")), nil
	}
	if text == "" {
		return textResult(errorf("text is required")), nil
	}

	if err := s.mgr.SendInput(ctx, name, text); err != nil {
		return textResult(errorf("send input to sandbox %q: %v", name, err)), nil
	}

	return textResult(fmt.Sprintf("Input sent to sandbox %q.", name)), nil
}

func (s *Server) handleSandboxReset(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	prompt := req.GetString("prompt", "")

	if name == "" {
		return textResult(errorf("name is required")), nil
	}

	// If a new prompt is provided, write it to prompt.txt before resetting.
	if prompt != "" {
		promptPath := sandbox.PromptFilePath(name)
		if err := os.WriteFile(promptPath, []byte(prompt), 0600); err != nil {
			return textResult(errorf("write prompt for sandbox %q: %v", name, err)), nil
		}
	}

	opts := sandbox.ResetOptions{
		Name:    name,
		Restart: true,
	}

	if err := s.mgr.Reset(ctx, opts); err != nil {
		return textResult(errorf("reset sandbox %q: %v", name, err)), nil
	}

	return textResult(fmt.Sprintf("Sandbox %q reset. Poll sandbox_status.", name)), nil
}

func (s *Server) handleSandboxFilesList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	if name == "" {
		return textResult(errorf("name is required")), nil
	}

	filesDir := sandbox.FilesDir(name)
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return textResult("(no files)"), nil
		}
		return textResult(errorf("list files for sandbox %q: %v", name, err)), nil
	}

	if len(entries) == 0 {
		return textResult("(no files)"), nil
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return textResult(strings.Join(names, "\n")), nil
}

func (s *Server) handleSandboxFilesRead(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	filename := req.GetString("filename", "")

	if name == "" {
		return textResult(errorf("name is required")), nil
	}
	if filename == "" {
		return textResult(errorf("filename is required")), nil
	}
	if err := validateFilename(filename); err != nil {
		return textResult(errorf("%v", err)), nil
	}

	path := filepath.Join(sandbox.FilesDir(name), filename)
	data, err := os.ReadFile(path) //nolint:gosec // path validated by validateFilename
	if err != nil {
		if os.IsNotExist(err) {
			return textResult(errorf("file %q not found in sandbox %q files", filename, name)), nil
		}
		return textResult(errorf("read file %q from sandbox %q: %v", filename, name, err)), nil
	}

	return textResult(string(data)), nil
}

func (s *Server) handleSandboxFilesWrite(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	filename := req.GetString("filename", "")
	content := req.GetString("content", "")

	if name == "" {
		return textResult(errorf("name is required")), nil
	}
	if filename == "" {
		return textResult(errorf("filename is required")), nil
	}
	if err := validateFilename(filename); err != nil {
		return textResult(errorf("%v", err)), nil
	}

	filesDir := sandbox.FilesDir(name)
	if err := os.MkdirAll(filesDir, 0750); err != nil {
		return textResult(errorf("create files dir for sandbox %q: %v", name, err)), nil
	}

	path := filepath.Join(filesDir, filename)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil { //nolint:gosec // path validated by validateFilename
		return textResult(errorf("write file %q to sandbox %q: %v", filename, name, err)), nil
	}

	return textResult(fmt.Sprintf("Written %d bytes to %s", len(content), filename)), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// textResult wraps a string as an MCP text content result.
func textResult(text string) *mcp.CallToolResult {
	return mcp.NewToolResultText(text)
}

// validateFilename rejects filenames with path separators or ".." to prevent
// path traversal attacks.
func validateFilename(filename string) error {
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") || strings.Contains(filename, "..") {
		return errors.New("filename must not contain path separators or '..'")
	}
	if filename == "." {
		return errors.New("filename must not be '.'")
	}
	return nil
}

// tailFile returns the last n lines of the file at path.
// Returns empty string if the file is empty or does not exist.
func tailFile(path string, n int) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is constructed from sandbox dir
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only file

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan log file: %w", err)
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return strings.Join(lines, "\n"), nil
}

// ── Help tool ─────────────────────────────────────────────────────────────────

func yoloaiHelpTool() mcp.Tool {
	return mcp.NewTool("yoloai_help",
		mcp.WithDescription("Returns the yoloAI workflow guide: how sandboxes work, the polling loop, how to handle agent questions, refinement, and when to apply or reset."),
	)
}

func handleYoloaiHelp(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return textResult(helpText), nil
}

const helpText = `# yoloAI MCP Server — Workflow Guide

yoloAI runs AI coding agents (Claude Code, Gemini, Codex) inside isolated
Docker containers. You are the outer agent. The inner agent works inside the
sandbox. Your job is to create sandboxes, monitor progress, handle questions,
inspect results, and surface diffs to the user for approval.

## Core concept

The sandbox is a safe scratchpad. The inner agent makes changes to a copy of
the working directory. Changes are NOT applied to the host until you (or the
user) explicitly applies them with 'yoloai apply'. You can inspect diffs,
request revisions, or discard the sandbox entirely.

## Basic workflow

1. sandbox_create — start a sandbox with a task prompt. Returns immediately.
2. Poll sandbox_status every 5 seconds until agent_status is done or failed.
3. sandbox_diff(stat=true) — cheap summary of what changed.
4. sandbox_diff / sandbox_diff_file — full diff when needed.
5. Surface the diff to the user. Get approval.
6. User runs 'yoloai apply <name>' from their terminal to apply changes.
7. sandbox_destroy — clean up.

## agent_status values

- active:  inner agent is currently working (tools running, code being written)
- idle:    inner agent finished its turn, waiting at the prompt
- done:    inner agent exited cleanly (task complete)
- failed:  inner agent exited with an error

When agent_status is done or failed, stop polling and inspect the diff.

## Handling agent questions

The inner agent may need to ask a question before it can continue. Protocol:

1. The inner agent writes /yoloai/files/question.json:
   {"question": "...", "context": "..."}
2. agent_status becomes idle (the agent is waiting, not active).
3. Call sandbox_files_list to check for question.json.
4. Call sandbox_files_read(name, "question.json") to read it.
5. Reason about the question or surface it to the user.
6. Call sandbox_files_write(name, "answer.json", '{"answer": "..."}').
7. Call sandbox_input(name, "I've answered your question in /yoloai/files/answer.json") to
   notify the agent.
8. Resume polling.

## Refinement (before destroy)

If the diff is close but not right, don't reset — refine:

  sandbox_input(name, "the email verification step was removed, add it back")

The inner agent receives the message at its prompt and continues working.
Resume polling after sending input. agent_status will go back to active.

## Poisoned context (reset)

If the inner agent has gone badly off track and the conversation context is
too confused to salvage:

  sandbox_reset(name)                          — restart with original prompt
  sandbox_reset(name, prompt="revised prompt") — restart with new instructions

The sandbox is wiped and started fresh. Resume polling after reset.

## Diff efficiency

Large diffs can be expensive to include in your context window:

  sandbox_diff(stat=true)    — one-liner summary: N files, M insertions, K deletions
  sandbox_diff               — full unified diff for all changed files
  sandbox_diff_file(path)    — diff for one specific file

Always call stat=true first. Only fetch the full diff or per-file diffs when
you need to reason about specific changes.

## Logs

  sandbox_log(name)          — last 100 lines of the inner agent's output
  sandbox_log(name, lines=N) — last N lines

Useful for debugging when agent_status is failed.

## Multiple sandboxes

You can run multiple sandboxes concurrently for parallel tasks. Each sandbox
is independent. Use sandbox_list to see all running sandboxes.

## What NOT to do

- Do not apply changes yourself via file tools. The diff/apply workflow
  exists so the user can review and approve changes. Surface the diff and
  let the user run 'yoloai apply'.
- Do not poll faster than 5 seconds — the inner agent needs time to work.
- Do not call sandbox_input while agent_status is active unless you intend
  to interrupt the current task.
`
