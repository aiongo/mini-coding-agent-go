package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/afero"
)

// MiniAgent mirrors Python MiniAgent (mini_coding_agent.py L225-258). It owns the model
// client, workspace snapshot, session store, and per-session config, and drives the ask()
// loop (component #5) over tools assembled from the six components.
type MiniAgent struct {
	// Raw flag-derived config. NewMiniAgent synthesizes the WorkspaceContext (from Cwd)
	// and the ModelClient (from Model/Host/Temperature/TopP/OllamaTimeout) out of these,
	// and resolves Resume into a Session. Populated via the With* options, typically from
	// miniAgentOptionsFromFlags.
	Cwd           string
	Model         string
	Host          string
	Temperature   float64
	TopP          float64
	OllamaTimeout int
	Resume        string

	ModelClient    ModelClient       // model_client (synthesized in NewMiniAgent)
	Logger         *slog.Logger      // logger (debug level; also forwarded to ModelClient)
	Workspace      *WorkspaceContext // workspace (synthesized in NewMiniAgent from Cwd)
	Root           string            // root = workspace.repo_root (Python: Path(workspace.repo_root))
	Fs             afero.Fs          // fs: filesystem behind all tool file IO; afero.NewOsFs() by default, injectable for tests
	SessionStore   *SessionStore     // session_store
	ApprovalPolicy string            // approval_policy
	MaxSteps       int               // max_steps
	MaxNewTokens   int               // max_new_tokens
	Depth          int               // depth
	MaxDepth       int               // max_depth
	ReadOnly       bool              // read_only
	Session        *Session          // session

	// Derived in Python __init__ from build_tools()/build_prefix()/session_store.save().
	Tools       map[string]*Tool
	Prefix      string
	SessionPath string
}

// NewMiniAgent constructs a MiniAgent from the session store plus options, applying the
// same defaults as Python MiniAgent.__init__ (mini_coding_agent.py L226-258). The
// WorkspaceContext is synthesized from Cwd and the ModelClient from the flag-derived config
// (Model/Host/Temperature/TopP/OllamaTimeout), mirroring Python build_agent(). If no session
// is provided via WithSession, a fresh one is created — mirroring Python's
// `session = session or {default dict}`.
func NewMiniAgent(opts ...MiniAgentOption) *MiniAgent {
	a := &MiniAgent{
		// SessionStore:   store,
		ApprovalPolicy: "ask",
		MaxSteps:       6,
		MaxNewTokens:   4096, // matches cli.go --max-new-tokens default; 512 truncates whole-file write_file calls
		Depth:          0,
		MaxDepth:       1,
		ReadOnly:       false,
	}
	for _, opt := range opts {
		opt(a)
	}
	a.Workspace = NewWorkspaceContext(a.Cwd)
	a.Root = a.Workspace.RepoRoot
	if a.Logger == nil {
		a.Logger = NewLogger(filepath.Join(a.Root, ".mini-coding-agent", "agent.log"))
	}
	a.Fs = afero.NewOsFs()
	a.SessionStore = NewSessionStore(filepath.Join(a.Root, ".mini-coding-agent", "sessions"))
	a.ModelClient = NewOllamaModelClient(a.Model, a.Host, a.Temperature, a.TopP, a.OllamaTimeout, a.Logger)
	if a.Session == nil {
		a.Session = newSession(a.Root)
	}
	// --resume resolves into the session here (Python build_agent's resume branch): "latest"
	// maps to the newest session file, any other value is a concrete id. A failure (bad id,
	// unreadable file) keeps the fresh session and logs a warning rather than crashing, so a
	// typo on --resume doesn't abort the whole agent. WithSession still wins when Resume is "".
	a.resolveResume()
	a.Tools = a.buildTools()
	a.Prefix = a.buildPrefix()
	// Persist the fresh/resumed session up front so SessionPath is set before the first Ask
	// (/session shows a path immediately) and a resumed session is re-saved under its id.
	a.SessionPath, _ = a.SessionStore.Save(a.Session)
	return a
}

// resolveResume consumes a.Resume: "latest" resolves to the newest session via
// SessionStore.Latest, any other non-empty value is a concrete session id. On success the
// loaded session replaces the fresh one and SessionPath is set to its file. On failure the
// fresh session is kept and a warning is logged — mirroring the intent of Python's resume
// path without making agent construction fail on a bad id.
func (a *MiniAgent) resolveResume() {
	if a.Resume == "" {
		return
	}
	id := a.Resume
	if id == "latest" {
		latest, err := a.SessionStore.Latest()
		if err != nil || latest == "" {
			a.Logger.Warn("resume: no latest session found", "err", err)
			return
		}
		id = latest
	}
	session, err := a.SessionStore.Load(id)
	if err != nil {
		a.Logger.Warn("resume: failed to load session, starting fresh", "id", id, "err", err)
		return
	}
	a.Session = session
	a.SessionPath = a.SessionStore.path(id)
}

// Remember appends item to bucket (an empty item is a no-op), moves an existing entry to
// the end, and returns only the last limit entries — most-recent-last. Mirrors Python
// remember(), the helper behind the working-memory buckets (tracked files and notes).
func Remember(bucket []string, item string, limit int) []string {
	if item == "" {
		return bucket
	}

	if slices.Contains(bucket, item) {
		bucket = slices.DeleteFunc(bucket, func(s string) bool {
			return s == item
		})
	}

	bucket = append(bucket, item)

	return bucket[len(bucket)-min(limit, len(bucket)):]
}

// toolOrder fixes the order the Tools block is rendered in buildPrefix. It mirrors Python
// build_tools' insertion order — Go map iteration is random, but Python's self.tools dict is
// insertion-ordered, and a stable Tools block matters for Ollama prompt caching.
var toolOrder = []string{
	"list_files", "read_file", "search", "run_shell", "write_file", "patch_file", "delegate",
}

// buildPrefix mirrors Python build_prefix (mini_coding_agent.py L333-374): assembles the
// stable prompt prefix — intro + rules + tool list + response examples + workspace snapshot.
// It does not change across steps (so it can be cached and reused); the mutable parts
// (memory, transcript, request) are appended per step in prompt() instead. Built once and
// stored on MiniAgent.Prefix at construction.
//
// Tool order is fixed via toolOrder (above). Each tool's schema field order is NOT stable,
// though — tool.Schema is a Go map (random iteration), whereas Python's schema dict is
// insertion-ordered. The output is correct semantically; for exact Python parity and full
// cache stability, Schema would need to be an ordered type (same caveat as WorkspaceContext's
// project_docs block).
func (a *MiniAgent) buildPrefix() string {
	toolLines := make([]string, 0, len(a.Tools))
	for _, name := range toolOrder {
		tool, ok := a.Tools[name]
		if !ok {
			continue // e.g. delegate absent when depth >= max_depth
		}
		fields := make([]string, 0, len(tool.Schema))
		for key, value := range tool.Schema {
			fields = append(fields, key+": "+value)
		}
		risk := "safe"
		if tool.Risky {
			risk = "approval required"
		}
		toolLines = append(toolLines, fmt.Sprintf("- %s(%s) [%s] %s",
			name, strings.Join(fields, ", "), risk, tool.Description))
	}
	toolText := strings.Join(toolLines, "\n")

	examples := strings.Join([]string{
		`<tool>{"name":"list_files","args":{"path":"."}}</tool>`,
		`<tool>{"name":"read_file","args":{"path":"README.md","start":1,"end":80}}</tool>`,
		"<tool name=\"write_file\" path=\"binary_search.py\"><content>def binary_search(nums, target):\n    return -1\n</content></tool>",
		"<tool name=\"patch_file\" path=\"binary_search.py\"><old_text>return -1</old_text><new_text>return mid</new_text></tool>",
		`<tool>{"name":"run_shell","args":{"command":"uv run --with pytest python -m pytest -q","timeout":20}}</tool>`,
		"<final>Done.</final>",
	}, "\n")

	rules := strings.Join([]string{
		"- Use tools instead of guessing about the workspace.",
		"- Return exactly one <tool>...</tool> or one <final>...</final>.",
		"- Tool calls must look like:",
		`  <tool>{"name":"tool_name","args":{...}}</tool>`,
		"- For write_file and patch_file with multi-line text, prefer XML style:",
		`  <tool name="write_file" path="file.py"><content>...</content></tool>`,
		"- Final answers must look like:",
		"  <final>your answer</final>",
		"- Never invent tool results.",
		"- Keep answers concise and concrete.",
		"- If the user asks you to create or update a specific file and the path is clear, use write_file or patch_file instead of repeatedly listing files.",
		"- Before writing tests for existing code, read the implementation first.",
		"- When writing tests, match the current implementation unless the user explicitly asked you to change the code.",
		"- New files should be complete and runnable, including obvious imports.",
		"- Do not repeat the same tool call with the same arguments if it did not help. Choose a different tool or return a final answer.",
		"- Required tool arguments must not be empty. Do not call read_file, write_file, patch_file, run_shell, or delegate with args={}.",
	}, "\n")

	return strings.Join([]string{
		"You are Mini-Coding-Agent, a small local coding agent running through Ollama.",
		"Rules:\n" + rules,
		"Tools:\n" + toolText,
		"Valid response examples:\n" + examples,
		a.Workspace.String(),
	}, "\n\n")
}

// buildTools mirrors Python build_tools (mini_coding_agent.py L282-328): assembles the tool
// registry — each tool's arg schema (param -> signature string like "str='.'" / "int=200"),
// risky flag, human description, and run callback. The schema/description feed build_prefix
// (component #2); the Run callbacks are the tool_* handlers in tools.go. delegate is
// registered only when depth < max_depth, and its handler stays a stub until the child
// agent's ask() (component #5) lands.
func (a *MiniAgent) buildTools() map[string]*Tool {
	tools := map[string]*Tool{
		"list_files": {
			Schema:      map[string]string{"path": "str='.'"},
			Risky:       false,
			Description: "List files in the workspace.",
			Run:         a.toolListFiles,
		},
		"read_file": {
			Schema:      map[string]string{"path": "str", "start": "int=1", "end": "int=200"},
			Risky:       false,
			Description: "Read a UTF-8 file by line range.",
			Run:         a.toolReadFile,
		},
		"search": {
			Schema:      map[string]string{"pattern": "str", "path": "str='.'"},
			Risky:       false,
			Description: "Search the workspace with rg or a simple fallback.",
			Run:         a.toolSearch,
		},
		"run_shell": {
			Schema:      map[string]string{"command": "str", "timeout": "int=20"},
			Risky:       true,
			Description: "Run a shell command in the repo root.",
			Run:         a.toolRunShell,
		},
		"write_file": {
			Schema:      map[string]string{"path": "str", "content": "str"},
			Risky:       true,
			Description: "Write a text file.",
			Run:         a.toolWriteFile,
		},
		"patch_file": {
			Schema:      map[string]string{"path": "str", "old_text": "str", "new_text": "str"},
			Risky:       true,
			Description: "Replace one exact text block in a file.",
			Run:         a.toolPatchFile,
		},
	}
	if a.Depth < a.MaxDepth {
		tools["delegate"] = &Tool{
			Schema:      map[string]string{"task": "str", "max_steps": "int=3"},
			Risky:       false,
			Description: "Ask a bounded read-only child agent to investigate.",
			Run:         a.toolDelegate,
		}
	}
	return tools
}

// Ask mirrors Python MiniAgent.ask (mini_coding_agent.py L444-494): the atomic per-request
// handler both CLI modes call. It records the user message, then loops model -> parse -> act:
// a "tool" result runs through runTool and is appended to the transcript; a "retry" feeds the
// corrective notice back as an assistant turn; a "final" is remembered and returned. The loop
// is bounded by MaxSteps (tool turns) and maxAttempts (total model calls); exiting without a
// final yields one of the STOP_* messages. Returns (final, error): the final answer text (always
// non-empty) plus any hard error (record/Complete failure), which the CLI prints to stderr.
func (a *MiniAgent) Ask(ctx context.Context, message string) (string, error) {
	memory := &a.Session.Memory
	if memory.Task == "" {
		memory.Task = Clip(strings.TrimSpace(message), 300)
	}
	if err := a.record(HistoryItem{Role: "user", Content: message, CreatedAt: Now()}); err != nil {
		return "", err
	}

	toolSteps := 0
	attempts := 0
	maxAttempts := max(a.MaxSteps*3, a.MaxSteps+4)
	for toolSteps < a.MaxSteps && attempts < maxAttempts {
		attempts++
		raw, err := a.ModelClient.Complete(ctx, a.prompt(message), a.MaxNewTokens)
		if err != nil {
			return "", err
		}
		switch pr := Parse(raw); pr.Kind {
		case "tool":
			toolSteps++
			name, _ := pr.Tool["name"].(string)
			args, _ := pr.Tool["args"].(map[string]any)
			if args == nil {
				args = map[string]any{}
			}
			result := a.runTool(ctx, name, args)
			if err := a.record(HistoryItem{
				Role: "tool", Name: name, Args: args, Content: result, CreatedAt: Now(),
			}); err != nil {
				return "", err
			}
			a.noteTool(name, args, result)
		case "retry":
			if err := a.record(HistoryItem{Role: "assistant", Content: pr.Notice, CreatedAt: Now()}); err != nil {
				return "", err
			}
		default: // "final"
			final := pr.Final
			if final == "" {
				final = strings.TrimSpace(raw)
			}
			if err := a.record(HistoryItem{Role: "assistant", Content: final, CreatedAt: Now()}); err != nil {
				return "", err
			}
			a.Session.Memory.Notes = Remember(a.Session.Memory.Notes, Clip(final, 220), 5)
			return final, nil
		}
	}

	// No <final> before the bound: too many malformed responses, or the step cap. The closing
	// record error is intentionally swallowed so the stop reason still reaches the caller.
	final := STOP_STEP_LIMIT
	if attempts >= maxAttempts && toolSteps < a.MaxSteps {
		final = STOP_TOO_MANY_MALFORMED
	}
	_ = a.record(HistoryItem{Role: "assistant", Content: final, CreatedAt: Now()})
	return final, nil
}

// reset mirrors Python MiniAgent.reset (mini_coding_agent.py L717-719): clear the transcript and
// working memory, then persist the emptied session.
func (a *MiniAgent) reset() {
	a.Session.History = []HistoryItem{}
	a.Session.Memory = Memory{Task: "", Files: []string{}, Notes: []string{}}
	_, _ = a.SessionStore.Save(a.Session)
}

// stdin is the shared buffered reader for all interactive input — the REPL prompt and the
// per-tool approval prompt both read from it. Sharing one reader matters: a freshly-created
// bufio.Reader would miss bytes the previous one buffered (or two would race), garbling input.
// Swappable (e.g. for tests feeding a fake stdin).
var stdin = bufio.NewReader(os.Stdin)

// readStdinLine reads one line (trailing CR/LF trimmed) from the shared stdin reader. The error
// (e.g. io.EOF) is returned alongside whatever was read; callers mirroring Python's input() treat
// any error as "no input" (EOFError -> deny / exit).
func readStdinLine() (string, error) {
	line, err := stdin.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// welcomeArt mirrors Python WELCOME_ART (mini_coding_agent.py L16-23). Lines with no
// backticks are raw string literals so the backslashes need no escaping; the two lines
// that contain a backtick use double-quoted literals (backtick is an ordinary char there).
var welcomeArt = []string{
	`/\     /\\`,
	"{  `---'  }",
	"{  O   O  }",
	"~~>  V  <~~",
	`\\  \|/  /`,
	"`-----'__",
}

// ljust left-aligns s to width, padding on the right with spaces, mirroring Python str.ljust.
// Width is measured in runes (matches Python len).
func ljust(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// center centers s to width, splitting the padding with the larger half on the right,
// mirroring Python str.center. Width is measured in runes.
func center(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	pad := width - n
	left := pad / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
}

// buildWelcome mirrors Python build_welcome (mini_coding_agent.py L869-909): renders the
// boxed startup banner — the ascii art, a title, and a workspace/model/branch/approval/
// session summary. width is fixed at 80 here: Python clamps the real terminal width to
// [68,84] (falling back to 80 when it can't detect it), and reading the real width needs
// a syscall (TIOCGWINSZ) or golang.org/x/term, neither pulled in yet to keep deps minimal.
func (a *MiniAgent) buildWelcome() string {
	const width = 80
	inner := width - 4
	const gap = 3
	leftWidth := (inner - gap) / 2
	rightWidth := inner - gap - leftWidth

	// row: "| " + middle(text, inner) left-aligned to inner + " |".
	row := func(text string) string {
		return "| " + ljust(Middle(text, inner), inner) + " |"
	}
	divider := func(ch string) string {
		return "+" + strings.Repeat(ch, width-2) + "+"
	}
	centerLine := func(text string) string {
		return "| " + center(Middle(text, inner), inner) + " |"
	}
	// cell renders "LABEL value" (label left-aligned to 9) clipped to size and re-padded.
	cell := func(label, value string, size int) string {
		return ljust(Middle(ljust(label, 9)+" "+value, size), size)
	}
	pair := func(leftLabel, leftValue, rightLabel, rightValue string) string {
		return "| " + cell(leftLabel, leftValue, leftWidth) +
			strings.Repeat(" ", gap) + cell(rightLabel, rightValue, rightWidth) + " |"
	}

	line := divider("=")
	rows := make([]string, 0, len(welcomeArt)+8)
	for _, art := range welcomeArt {
		rows = append(rows, centerLine(art))
	}
	rows = append(rows,
		centerLine("MINI CODING AGENT"),
		divider("-"),
		row(""),
		row("WORKSPACE  "+Middle(a.Workspace.Cwd, inner-11)),
		pair("MODEL", a.Model, "BRANCH", a.Workspace.Branch),
		pair("APPROVAL", a.ApprovalPolicy, "SESSION", a.Session.ID),
		row(""),
	)

	all := make([]string, 0, len(rows)+2)
	all = append(all, line)
	all = append(all, rows...)
	all = append(all, line)
	return strings.Join(all, "\n")
}

// memoryText mirrors Python memory_text (mini_coding_agent.py L376-385): renders the working
// memory block (task / files / notes) for the prompt. An empty task or files list collapses to
// "-", and an empty notes list renders as "- none" — matching Python's `x or '-'` /
// `"...".join(...) or "- none"` fallbacks.
func (a *MiniAgent) memoryText() string {
	m := a.Session.Memory
	noteLines := make([]string, 0, len(m.Notes))
	for _, note := range m.Notes {
		noteLines = append(noteLines, "- "+note)
	}
	notes := strings.Join(noteLines, "\n")
	if notes == "" {
		notes = "- none"
	}
	task := m.Task
	if task == "" {
		task = "-"
	}
	files := strings.Join(m.Files, ", ")
	if files == "" {
		files = "-"
	}
	return strings.Join([]string{
		"Memory:",
		"- task: " + task,
		"- files: " + files,
		"- notes:",
		notes,
	}, "\n")
}

// historyText mirrors Python history_text (mini_coding_agent.py L390-417): renders the
// transcript with per-freshness clipping and read_file deduplication. The last 6 entries are
// "recent" (clip 900); older entries are clipped to 180 (tool) / 220 (user/assistant). A
// non-recent read_file is skipped if its path was already seen — unless a write_file/
// patch_file on the same path cleared the seen set (so a read after a write is never hidden).
// The whole rendered transcript is then capped to maxHistory.
//
// Deviation from the Python format: Python renders tool args with json.dumps(...,
// sort_keys=True), which uses ", "/": " separators (e.g. {"path": "x"}). Go's json.Marshal
// sorts map keys (matching sort_keys=True) but emits compact separators ({"path":"x"}). No
// test pins the whitespace, and the dedup/clip behavior is what the tests assert, so the
// compact form is kept. A nil args map is rendered as "{}" to match json.dumps({}).
func (a *MiniAgent) historyText() string {
	history := a.Session.History
	if len(history) == 0 {
		return "- empty"
	}

	var lines []string
	seenReads := map[string]struct{}{}
	recentStart := max(0, len(history)-6)
	for index, item := range history {
		recent := index >= recentStart
		if item.Role == "tool" && (item.Name == "write_file" || item.Name == "patch_file") {
			delete(seenReads, argString(item.Args, "path", ""))
		}
		if item.Role == "tool" && item.Name == "read_file" && !recent {
			path := argString(item.Args, "path", "")
			if _, seen := seenReads[path]; seen {
				continue
			}
			seenReads[path] = struct{}{}
		}

		if item.Role == "tool" {
			limit := 180
			if recent {
				limit = 900
			}
			args := item.Args
			if args == nil {
				args = map[string]any{}
			}
			b, _ := json.Marshal(args)
			lines = append(lines, fmt.Sprintf("[tool:%s] %s", item.Name, string(b)))
			lines = append(lines, Clip(item.Content, limit))
		} else {
			limit := 220
			if recent {
				limit = 900
			}
			lines = append(lines, fmt.Sprintf("[%s] %s", item.Role, Clip(item.Content, limit)))
		}
	}
	return Clip(strings.Join(lines, "\n"), MAX_HISTORY)
}

// prompt mirrors Python prompt (mini_coding_agent.py L422-428): joins the stable prefix, the
// memory block, the transcript, and the current user request with blank-line separators. The
// prefix is built once (buildPrefix); memory/transcript/request are the mutable parts appended
// per step, so the prefix stays cache-friendly.
func (a *MiniAgent) prompt(userMessage string) string {
	return strings.Join([]string{
		a.Prefix,
		a.memoryText(),
		"Transcript:\n" + a.historyText(),
		"Current user request:\n" + userMessage,
	}, "\n\n")
}

// record mirrors Python record (mini_coding_agent.py L433-435): append a transcript entry to
// the session history and persist it, recording the session file path. Python's
// SessionStore.save raises on failure and lets it propagate; the Go SessionStore.Save returns
// an error, so record returns it and the ask loop decides what to do. The history append
// happens before the save, matching Python — the entry is already in history by the time the
// save runs, so a save failure still leaves the in-memory history updated.
func (a *MiniAgent) record(item HistoryItem) error {
	a.Session.History = append(a.Session.History, item)
	path, err := a.SessionStore.Save(a.Session)
	if err != nil {
		return err
	}
	a.SessionPath = path
	return nil
}

// noteTool mirrors Python note_tool (mini_coding_agent.py L437-443): update working memory
// after a tool runs — remember the file path for read_file/write_file/patch_file (when the
// path is non-empty), and push a one-line note with newlines flattened to spaces and clipped
// to 220. Remember returns a new slice (the Go-idiomatic replacement for Python's in-place
// list mutation), so the results are assigned back into the session memory.
func (a *MiniAgent) noteTool(name string, args map[string]any, result string) {
	m := &a.Session.Memory
	if name == "read_file" || name == "write_file" || name == "patch_file" {
		if path := argString(args, "path", ""); path != "" {
			m.Files = Remember(m.Files, path, 8)
		}
	}
	note := fmt.Sprintf("%s: %s", name, Clip(strings.ReplaceAll(result, "\n", " "), 220))
	m.Notes = Remember(m.Notes, note, 5)
}
