// Command mini-coding-agent-go is a minimal local coding agent for Ollama models, a Go
// port of rasbt/mini-coding-agent with no LLM framework. The root command runs an
// interactive REPL; the prompt subcommand (alias p) runs a single prompt. The agent
// drives a bounded tool loop (list/read/search/write/patch/shell/delegate) over a
// workspace and persists resumable sessions under .mini-coding-agent/. See README.md.
package main

import "os"

func main() {
	if err := NewCli().Run(); err != nil {
		// urfave/cli already prints usage errors; just propagate the exit code
		// so callers (and CI) can tell the invocation failed.
		os.Exit(1)
	}
}
