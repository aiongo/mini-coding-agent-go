package main

import "strings"

var (
	DOC_NAMES   = []string{"AGENTS.md", "README.md", "pyproject.toml", "package.json"}
	HELP_TEXT   = "/help, /memory, /session, /reset, /exit"
	WELCOME_ART = []string{
		"/\\     /\\\\",
		"{  `---'  }",
		"{  O   O  }",
		"~~>  V  <~~",
		"\\\\  \\|/  /",
		"`-----'__",
	}
	HELP_DETAILS = strings.Join([]string{

		"Commands:",
		"/help    Show this help message.",
		"/memory  Show the agent's distilled working memory.",
		"/session Show the path to the saved session file.",
		"/reset   Clear the current session history and memory.",
		"/exit    Exit the agent.",
	}, "\n")

	MAX_TOOL_OUTPUT    = 4000
	MAX_HISTORY        = 12000
	IGNORED_PATH_NAMES = []string{".git", ".mini-coding-agent", "__pycache__", ".pytest_cache", ".ruff_cache", ".venv", "venv"}

	// Ask-loop stop messages (mini_coding_agent.py L486-492): emitted when the loop exits
	// without a <final> — either too many malformed model responses, or the step cap reached.
	STOP_TOO_MANY_MALFORMED = "Stopped after too many malformed model responses without a valid tool call or final answer."
	STOP_STEP_LIMIT         = "Stopped after reaching the step limit without a final answer."
)
