package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/afero"
)

// Tool mirrors one entry of Python's self.tools dict (mini_coding_agent.py L282-328):
// a named tool with an arg schema, a risky flag, a human description, and a run callback.
type Tool struct {
	Schema      map[string]string
	Risky       bool
	Description string
	Run         ToolFunc
}

// ToolFunc is the shape of a tool's run callback: takes the caller's context and the parsed
// args map, returns the (to-be-clipped) text result or an error. Matches Python tool_*
// methods such as tool_read_file(self, args) -> str, which raise on failure. The Go-idiomatic
// addition over Python is the context — it lets a REPL Ctrl-C interrupt an in-flight
// run_shell/rg/delegate instead of waiting out their timeout.
type ToolFunc func(ctx context.Context, args map[string]any) (string, error)

// All filesystem access in the tool handlers goes through afero — specifically MiniAgent.Fs
// (initialized to afero.NewOsFs in NewMiniAgent), never the os package directly. This keeps
// file IO behind an injectable Fs so the handlers can be exercised against a fake filesystem.
// os.SameFile is the one exception (see pathIsWithinRoot): afero has no samefile concept,
// since dev+inode identity is an OS-level notion that an abstract Fs cannot express.

// --- arg helpers ---------------------------------------------------------------

// The model's tool args arrive as a parsed JSON map, so numbers come through as float64
// (encoding/json decodes every number to float64). These mirror Python's args.get(key,
// default) + int()/str() coercion used inside the tool_* methods.

func argString(args map[string]any, key, def string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

func argInt(args map[string]any, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// --- path sandboxing (Python path / path_is_within_root, L722-740) -------------

// pathIsWithinRoot mirrors Python path_is_within_root (L722-732): if `resolved` does not
// exist, walk up to the nearest existing ancestor, then check whether any ancestor from
// there up is the same file (dev+inode) as the workspace root.
//
// Existence checks go through a.Fs; the final identity check uses os.SameFile, which afero
// cannot express — afero.Fs.Stat returns the real os.FileInfo under NewOsFs, so SameFile
// works there (it would not under an in-memory Fs, but samefile is an OS notion anyway).
func (a *MiniAgent) pathIsWithinRoot(resolved string) bool {
	probe := resolved
	for {
		if _, err := a.Fs.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	rootInfo, err := a.Fs.Stat(a.Root)
	if err != nil {
		return false
	}
	for candidate := probe; ; {
		if info, err := a.Fs.Stat(candidate); err == nil {
			if os.SameFile(info, rootInfo) {
				return true
			}
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
		candidate = parent
	}
	return false
}

// path mirrors Python path (L734-740): resolve rawPath against the workspace root (absolute
// paths used as-is, relative joined to root), resolve symlinks, and reject anything that
// escapes the workspace. Returns the resolved absolute path.
func (a *MiniAgent) path(rawPath string) (string, error) {
	p := rawPath
	if !filepath.IsAbs(p) {
		p = filepath.Join(a.Root, p)
	}
	resolved := resolvePath(p)
	if !a.pathIsWithinRoot(resolved) {
		return "", fmt.Errorf("path escapes workspace: %s", rawPath)
	}
	return resolved, nil
}

// rel returns resolved relative to the workspace root (best-effort), for display — mirrors
// Python's Path.relative_to(self.root) used in tool output.
func (a *MiniAgent) rel(resolved string) string {
	if rel, err := filepath.Rel(a.Root, resolved); err == nil {
		return rel
	}
	return resolved
}

// --- tool handlers (Python tool_*, L742-866) -----------------------------------

// toolListFiles mirrors Python tool_list_files (L742-754): list non-ignored entries in a
// directory (dirs first, then by name), capped at 200, as "[D]/[F] <rel>" lines.
func (a *MiniAgent) toolListFiles(ctx context.Context, args map[string]any) (string, error) {
	path, err := a.path(argString(args, "path", "."))
	if err != nil {
		return "", err
	}
	info, err := a.Fs.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("path is not a directory")
	}
	entries, err := afero.ReadDir(a.Fs, path)
	if err != nil {
		return "", err
	}
	// dirs first (dir -> -1), then case-insensitive name — matches Python key (is_file, name).
	slices.SortFunc(entries, func(x, y os.FileInfo) int {
		dx, dy := x.IsDir(), y.IsDir()
		switch {
		case dx && !dy:
			return -1
		case !dx && dy:
			return 1
		default:
			return strings.Compare(strings.ToLower(x.Name()), strings.ToLower(y.Name()))
		}
	})
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if slices.Contains(IGNORED_PATH_NAMES, e.Name()) {
			continue
		}
		kind := "[D]"
		if !e.IsDir() {
			kind = "[F]"
		}
		lines = append(lines, kind+" "+a.rel(filepath.Join(path, e.Name())))
		if len(lines) >= 200 {
			break
		}
	}
	if len(lines) == 0 {
		return "(empty)", nil
	}
	return strings.Join(lines, "\n"), nil
}

// toolReadFile mirrors Python tool_read_file (L756-766): read a UTF-8 file by [start,end]
// line range, rendering as "<n>: <line>" with a "# <rel>" header. A second "# ..." header
// line states the "<n>:" prefixes are tool-added line numbers, not file content — weak
// models otherwise mistake them for literal data (e.g. parsing a one-int-per-line file as
// "index: value").
func (a *MiniAgent) toolReadFile(ctx context.Context, args map[string]any) (string, error) {
	rawPath := argString(args, "path", "")
	if rawPath == "" {
		return "", fmt.Errorf("path is required")
	}
	path, err := a.path(rawPath)
	if err != nil {
		return "", err
	}
	info, err := a.Fs.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a file")
	}
	start := argInt(args, "start", 1)
	end := argInt(args, "end", 200)
	if start < 1 || end < start {
		return "", fmt.Errorf("invalid line range")
	}
	if end-start+1 > MAX_READ_LINES {
		return "", fmt.Errorf("invalid line range: at most %d lines per read", MAX_READ_LINES)
	}
	data, err := afero.ReadFile(a.Fs, path)
	if err != nil {
		return "", err
	}
	lines := splitLines(strings.ToValidUTF8(string(data), "�"))
	lo := min(start-1, len(lines))
	hi := min(end, len(lines))
	out := make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, fmt.Sprintf("%4d: %s", i+1, lines[i]))
	}
	return fmt.Sprintf(
		"# %s\n# (the line-number prefixes below, e.g. \"   1:\", are added by this tool for reference; they are NOT part of the file content)\n%s",
		a.rel(path), strings.Join(out, "\n"),
	), nil
}

// toolSearch mirrors Python tool_search (L768-794): ripgrep if available, else a stdlib
// substring fallback. Caps at 200 matches.
func (a *MiniAgent) toolSearch(ctx context.Context, args map[string]any) (string, error) {
	pattern := strings.TrimSpace(argString(args, "pattern", ""))
	if pattern == "" {
		return "", fmt.Errorf("pattern must not be empty")
	}
	path, err := a.path(argString(args, "path", "."))
	if err != nil {
		return "", err
	}
	// Prefer ripgrep, falling back to the stdlib walker when rg isn't installed. No
	// exec.LookPath pre-probe — RunProcess reports a missing binary as *CmdNotFoundError,
	// which IsCmdNotFoundError detects.
	res, runErr := RunProcess(ctx, "rg",
		[]string{"-n", "--smart-case", "--max-count", "200", pattern, path}, a.Root, 0)
	if IsCmdNotFoundError(runErr) {
		return a.searchFallback(path, pattern)
	}
	if out := strings.TrimSpace(res.Stdout); out != "" {
		return out, nil
	}
	if e := strings.TrimSpace(res.Stderr); e != "" {
		return e, nil
	}
	return "(no matches)", nil
}

// searchFallback is the no-rg path of Python tool_search: walk files (skipping ignored
// dirs), case-insensitive substring match per line, cap 200.
func (a *MiniAgent) searchFallback(root, pattern string) (string, error) {
	matches := make([]string, 0, 64)
	needle := strings.ToLower(pattern)
	stopErr := fmt.Errorf("search cap reached")
	walkErr := afero.Walk(a.Fs, root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if slices.Contains(IGNORED_PATH_NAMES, info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if slices.Contains(IGNORED_PATH_NAMES, info.Name()) {
			return nil
		}
		data, e := afero.ReadFile(a.Fs, p)
		if e != nil {
			return nil
		}
		relPath := a.rel(p)
		for i, line := range splitLines(strings.ToValidUTF8(string(data), "�")) {
			if strings.Contains(strings.ToLower(line), needle) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", relPath, i+1, line))
				if len(matches) >= 200 {
					return stopErr
				}
			}
		}
		return nil
	})
	_ = walkErr // stopErr is the expected early-exit signal; other walk errors are ignored
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(matches, "\n"), nil
}

// toolRunShell mirrors Python tool_run_shell (L796-819): run a shell command in the repo
// root with a bounded timeout, returning exit_code + stdout + stderr. Python uses
// shell=True; RunProcess runs it as `sh -c <command>`.
func (a *MiniAgent) toolRunShell(ctx context.Context, args map[string]any) (string, error) {
	command := strings.TrimSpace(argString(args, "command", ""))
	if command == "" {
		return "", fmt.Errorf("command must not be empty")
	}
	timeout := argInt(args, "timeout", 20)
	if timeout < 1 || timeout > 120 {
		return "", fmt.Errorf("timeout must be in [1, 120]")
	}
	res, err := RunProcess(ctx, "sh", []string{"-c", command}, a.Root, timeout)
	if err != nil {
		return "", err
	}
	stdout := strings.TrimSpace(res.Stdout)
	if stdout == "" {
		stdout = "(empty)"
	}
	stderr := strings.TrimSpace(res.Stderr)
	if stderr == "" {
		stderr = "(empty)"
	}
	return fmt.Sprintf("exit_code: %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, stdout, stderr), nil
}

// toolWriteFile mirrors Python tool_write_file (L821-826): create parent dirs and write a
// UTF-8 text file, returning a "wrote <rel> (n chars)" confirmation.
func (a *MiniAgent) toolWriteFile(ctx context.Context, args map[string]any) (string, error) {
	rawPath := argString(args, "path", "")
	if rawPath == "" {
		return "", fmt.Errorf("path is required")
	}
	path, err := a.path(rawPath)
	if err != nil {
		return "", err
	}
	content, ok := args["content"]
	if !ok {
		return "", fmt.Errorf("missing content")
	}
	s, ok := content.(string)
	if !ok {
		// Python path.write_text(args["content"]) raises TypeError on a non-str, which run_tool
		// folds into "error: tool write_file failed: ...". Match that instead of silently
		// writing an empty file (the comma-ok form would zero content to "").
		return "", fmt.Errorf("content must be a string")
	}
	if err := a.Fs.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := afero.WriteFile(a.Fs, path, []byte(s), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s (%d chars)", a.rel(path), utf8.RuneCountInString(s)), nil
}

// toolPatchFile mirrors Python tool_patch_file (L828-842): replace exactly one occurrence
// of old_text with new_text in a file, rejecting if old_text is absent or non-unique.
func (a *MiniAgent) toolPatchFile(ctx context.Context, args map[string]any) (string, error) {
	rawPath := argString(args, "path", "")
	if rawPath == "" {
		return "", fmt.Errorf("path is required")
	}
	path, err := a.path(rawPath)
	if err != nil {
		return "", err
	}
	info, err := a.Fs.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a file")
	}
	oldText := argString(args, "old_text", "")
	if oldText == "" {
		return "", fmt.Errorf("old_text must not be empty")
	}
	newValue, ok := args["new_text"]
	if !ok {
		return "", fmt.Errorf("missing new_text")
	}
	newText, ok := newValue.(string)
	if !ok {
		return "", fmt.Errorf("new_text must be a string")
	}
	data, err := afero.ReadFile(a.Fs, path)
	if err != nil {
		return "", err
	}
	text := string(data)
	if count := strings.Count(text, oldText); count != 1 {
		return "", fmt.Errorf("old_text must occur exactly once, found %d", count)
	}
	patched := strings.Replace(text, oldText, newText, 1)
	if err := afero.WriteFile(a.Fs, path, []byte(patched), 0o644); err != nil {
		return "", err
	}
	return "patched " + a.rel(path), nil
}

// toolDelegate mirrors Python tool_delegate (mini_coding_agent.py L847-866): spawn a bounded,
// read-only child agent and return its final answer prefixed with "delegate_result:". The child
// is built from the same flag-derived config (so it reaches the same Ollama/workspace), then its
// ModelClient/Workspace/Root/Fs/SessionStore are overwritten with the parent's instances — matching
// Python, where the child shares those objects instead of re-creating them. The child runs with
// approval_policy="never", read_only=true, depth+1, and max_steps from the call (default 3); its
// memory is seeded with the task and a clipped snapshot of the parent's transcript as notes.
func (a *MiniAgent) toolDelegate(ctx context.Context, args map[string]any) (string, error) {
	if a.Depth >= a.MaxDepth {
		return "", fmt.Errorf("delegate depth exceeded")
	}
	task := strings.TrimSpace(argString(args, "task", ""))
	if task == "" {
		return "", fmt.Errorf("task must not be empty")
	}
	child := NewMiniAgent(
		WithCwd(a.Cwd),
		WithModel(a.Model),
		WithHost(a.Host),
		WithTemperature(a.Temperature),
		WithTopP(a.TopP),
		WithOllamaTimeout(a.OllamaTimeout),
		WithApprovalPolicy("never"),
		WithMaxSteps(argInt(args, "max_steps", 3)),
		WithMaxNewTokens(a.MaxNewTokens),
		WithDepth(a.Depth+1),
		WithMaxDepth(a.MaxDepth),
		WithReadOnly(true),
		WithLogger(a.Logger),
	)
	// Reuse the parent's live client/workspace/store/fs (Python child shares the same instances,
	// rather than re-deriving them). The child keeps its own fresh session.
	child.ModelClient = a.ModelClient
	child.Workspace = a.Workspace
	child.Root = a.Root
	child.Fs = a.Fs
	child.SessionStore = a.SessionStore

	child.Session.Memory.Task = task
	child.Session.Memory.Notes = []string{Clip(a.historyText(), 300)}

	// The child runs on the parent's ctx so a REPL Ctrl-C cancels the delegate's model calls
	// too; it is still bounded by its own MaxSteps and the model call timeout.
	final, err := child.Ask(ctx, task)
	if err != nil {
		return "", err
	}
	return "delegate_result:\n" + final, nil
}

// splitLines mirrors Python str.splitlines(): splits on \n (after normalizing CRLF), and
// drops a single trailing empty element produced by a final newline.
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// --- validation, permissions, dispatch (Python run_tool / validate_tool / approve, L496-613) ---

// validateTool mirrors Python MiniAgent.validate_tool (mini_coding_agent.py L536-600): pre-flight
// argument + filesystem checks BEFORE a tool runs. It reuses a.path for sandbox containment and
// a.Fs for existence checks, raising (returning an error for) the same messages Python does. A
// missing required key surfaces as a "'<key>'" error (Python's KeyError text, caught by run_tool).
func (a *MiniAgent) validateTool(name string, args map[string]any) error {
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case "list_files":
		p, err := a.path(argString(args, "path", "."))
		if err != nil {
			return err
		}
		if info, err := a.Fs.Stat(p); err != nil || !info.IsDir() {
			return fmt.Errorf("path is not a directory")
		}
	case "read_file":
		rawPath := argString(args, "path", "")
		if rawPath == "" {
			return fmt.Errorf("'path'")
		}
		p, err := a.path(rawPath)
		if err != nil {
			return err
		}
		if info, err := a.Fs.Stat(p); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("path is not a file")
		}
		start := argInt(args, "start", 1)
		end := argInt(args, "end", 200)
		if start < 1 || end < start {
			return fmt.Errorf("invalid line range")
		}
		if end-start+1 > MAX_READ_LINES {
			return fmt.Errorf("invalid line range: at most %d lines per read", MAX_READ_LINES)
		}
	case "search":
		pattern := strings.TrimSpace(argString(args, "pattern", ""))
		if pattern == "" {
			return fmt.Errorf("pattern must not be empty")
		}
		if _, err := a.path(argString(args, "path", ".")); err != nil {
			return err
		}
	case "run_shell":
		command := strings.TrimSpace(argString(args, "command", ""))
		if command == "" {
			return fmt.Errorf("command must not be empty")
		}
		timeout := argInt(args, "timeout", 20)
		if timeout < 1 || timeout > 120 {
			return fmt.Errorf("timeout must be in [1, 120]")
		}
	case "write_file":
		rawPath := argString(args, "path", "")
		if rawPath == "" {
			return fmt.Errorf("'path'")
		}
		p, err := a.path(rawPath)
		if err != nil {
			return err
		}
		if info, err := a.Fs.Stat(p); err == nil && info.IsDir() {
			return fmt.Errorf("path is a directory")
		}
		if _, ok := args["content"]; !ok {
			return fmt.Errorf("missing content")
		}
	case "patch_file":
		rawPath := argString(args, "path", "")
		if rawPath == "" {
			return fmt.Errorf("'path'")
		}
		p, err := a.path(rawPath)
		if err != nil {
			return err
		}
		info, err := a.Fs.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("path is not a file")
		}
		oldText := argString(args, "old_text", "")
		if oldText == "" {
			return fmt.Errorf("old_text must not be empty")
		}
		if _, ok := args["new_text"]; !ok {
			return fmt.Errorf("missing new_text")
		}
		data, err := afero.ReadFile(a.Fs, p)
		if err != nil {
			return err
		}
		if count := strings.Count(string(data), oldText); count != 1 {
			return fmt.Errorf("old_text must occur exactly once, found %d", count)
		}
	case "delegate":
		if a.Depth >= a.MaxDepth {
			return fmt.Errorf("delegate depth exceeded")
		}
		task := strings.TrimSpace(argString(args, "task", ""))
		if task == "" {
			return fmt.Errorf("task must not be empty")
		}
	}
	return nil
}

// repeatedToolCall mirrors Python MiniAgent.repeated_tool_call (mini_coding_agent.py L517-522):
// true when the last two recorded tool events both match (name, args) — i.e. the model is
// re-issuing the exact same call. args equality is deep (reflect.DeepEqual on the maps).
func (a *MiniAgent) repeatedToolCall(name string, args map[string]any) bool {
	var toolEvents []HistoryItem
	for _, item := range a.Session.History {
		if item.Role == "tool" {
			toolEvents = append(toolEvents, item)
		}
	}
	if len(toolEvents) < 2 {
		return false
	}
	for _, item := range toolEvents[len(toolEvents)-2:] {
		if item.Name != name || !reflect.DeepEqual(item.Args, args) {
			return false
		}
	}
	return true
}

// toolExample mirrors Python MiniAgent.tool_example (mini_coding_agent.py L524-534): the canonical
// call string for a tool, appended to an "invalid arguments" error to steer the model back.
func (a *MiniAgent) toolExample(name string) string {
	switch name {
	case "list_files":
		return `<tool>{"name":"list_files","args":{"path":"."}}</tool>`
	case "read_file":
		return `<tool>{"name":"read_file","args":{"path":"README.md","start":1,"end":80}}</tool>`
	case "search":
		return `<tool>{"name":"search","args":{"pattern":"binary_search","path":"."}}</tool>`
	case "run_shell":
		return `<tool>{"name":"run_shell","args":{"command":"uv run --with pytest python -m pytest -q","timeout":20}}</tool>`
	case "write_file":
		return "<tool name=\"write_file\" path=\"binary_search.py\"><content>def binary_search(nums, target):\n    return -1\n</content></tool>"
	case "patch_file":
		return "<tool name=\"patch_file\" path=\"binary_search.py\"><old_text>return -1</old_text><new_text>return mid</new_text></tool>"
	case "delegate":
		return `<tool>{"name":"delegate","args":{"task":"inspect README.md","max_steps":3}}</tool>`
	}
	return ""
}

// approve mirrors Python MiniAgent.approve (mini_coding_agent.py L602-613): read_only always denies;
// "auto" always grants; "never" always denies; "ask" prompts on the shared stdin reader and accepts
// y/yes. An EOF/empty line (readStdinLine error) denies, matching Python's EOFError -> False.
func (a *MiniAgent) approve(name string, args map[string]any) bool {
	if a.ReadOnly {
		return false
	}
	switch a.ApprovalPolicy {
	case "auto":
		return true
	case "never":
		return false
	}
	b, _ := json.Marshal(args)
	fmt.Printf("approve %s %s? [y/N] ", name, string(b))
	line, err := readStdinLine()
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// runTool mirrors Python MiniAgent.run_tool (mini_coding_agent.py L496-515): the single dispatch
// path every tool call goes through. It NEVER returns an error — every failure (unknown tool,
// invalid args, repeated call, denied approval, runtime crash) is folded into an "error: ..." string
// that becomes the tool's transcript content, so the ask loop keeps looping and the model can react.
func (a *MiniAgent) runTool(ctx context.Context, name string, args map[string]any) string {
	tool, ok := a.Tools[name]
	if !ok {
		return fmt.Sprintf("error: unknown tool '%s'", name)
	}
	if err := a.validateTool(name, args); err != nil {
		message := fmt.Sprintf("error: invalid arguments for %s: %s", name, err)
		if example := a.toolExample(name); example != "" {
			message += "\nexample: " + example
		}
		return message
	}
	if a.repeatedToolCall(name, args) {
		return fmt.Sprintf("error: repeated identical tool call for %s; choose a different tool or return a final answer", name)
	}
	if tool.Risky && !a.approve(name, args) {
		return fmt.Sprintf("error: approval denied for %s", name)
	}
	result, err := tool.Run(ctx, args)
	if err != nil {
		return fmt.Sprintf("error: tool %s failed: %s", name, err)
	}
	return Clip(result, MAX_TOOL_OUTPUT)
}
