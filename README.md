&nbsp;
# mini-coding-agent-go

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.27-00ADD8.svg)](go.mod)

**A minimal local coding agent in Go — a faithful port of
[Sebastian Raschka's mini-coding-agent](https://github.com/rasbt/mini-coding-agent),
with no LLM framework.**

**English** | [简体中文](README.zh-CN.md)

This repo contains a small standalone coding agent:

- code: a single `main` package (`mini_agent.go`, `tools.go`, `model.go`, ...)
- CLI: `mini-coding-agent-go`

It is a minimal local agent loop with:

- workspace snapshot collection
- stable prompt plus turn state
- structured tools
- approval handling for risky tools
- transcript and memory persistence
- bounded delegation

The model backend is currently based on Ollama.

<a href="https://magazine.sebastianraschka.com/p/components-of-a-coding-agent">
  <img src="https://substack-post-media.s3.amazonaws.com/public/images/49b97718-57f4-4977-99c8-8ad5c4d32af3_1548x862.png" width="500px">
</a>

<br>

**[The detailed tutorial: Components of a Coding Agent](https://magazine.sebastianraschka.com/p/components-of-a-coding-agent)**
— this port follows the Python original component by component.

&nbsp;
## Differences from the Python Original

The Go port keeps the agent's internals — the custom `<tool>`/`<final>` text protocol, the
ask() loop, the six components below — and only adjusts the surroundings:

- **CLI shape**: the root command starts the interactive REPL; a one-shot prompt goes
  through a `prompt` subcommand (alias `p`) instead of a root-level positional argument.
- **No runtime framework**: resty for Ollama's raw `/api/generate` endpoint,
  `urfave/cli` for the CLI, `afero` for file IO. No LLM framework, no Ollama SDK.
- **`--max-new-tokens` defaults to 4096** (the Python default of 512 truncates whole-file
  `write_file` calls).
- **Debug log**: model request/response traffic is appended to
  `.mini-coding-agent/agent.log` under the workspace root.
- Sessions, working memory, approval gates, the seven tools, and the REPL slash commands
  match the original.

&nbsp;
## Six Core Components

<a href="https://magazine.sebastianraschka.com/p/components-of-a-coding-agent">
  <img alt="Six core components of a coding agent" src="https://sebastianraschka.com/images/github/mini-coding-agent/six-components.webp" width="500px">
</a>

This coding harness is organized around six practical building blocks:

1. **Live repo context**
   The agent collects stable workspace facts upfront, such as repo layout, instructions, and git state.
2. **Prompt shape and cache reuse**
   A stable prompt prefix, which is separate from the changing request, transcript, and memory so repeated model calls can reuse the static parts efficiently.
3. **Structured tools, validation, and permissions**
   The model works through named tools with checked inputs, workspace path validation, and approval gates instead of free-form arbitrary actions.
4. **Context reduction and output management**
   Long outputs are clipped, repeated reads are deduplicated, and older transcript entries are compressed to keep prompt size under control.
5. **Transcripts, memory, and resumption**
   The runtime keeps both a full durable transcript and a smaller working memory so sessions can be resumed while preserving important state via working memory.
6. **Delegation and bounded subagents**
   Scoped subtasks can be delegated to helper agents that inherit enough context to help (but operate within limits).

&nbsp;
## Requirements

You need:

- Go 1.27+
- Ollama installed
- an Ollama model pulled locally

&nbsp;
## Install Ollama

Install Ollama on your machine so the `ollama` command is available in your shell.

Official installation link: [ollama.com/download](https://ollama.com/download)

Then verify:

```bash
ollama --help
```

Start the server:

```bash
ollama serve
```

In another terminal, pull the default model used by this project:

```bash
ollama pull gemma4:cloud
```

The agent just sends prompts to Ollama's `/api/generate` endpoint, so it also works with
any other model exposed by your Ollama instance.

&nbsp;
## Project Setup

Clone the repo or your fork and change into it:

```bash
git clone https://github.com/aiongo/mini-coding-agent-go.git
cd mini-coding-agent-go
```

Build the binary:

```bash
go build        # produces ./mini-coding-agent-go
```

Or install it directly into your `GOBIN`:

```bash
go install github.com/aiongo/mini-coding-agent-go@latest
```

&nbsp;
## Basic Usage

Start the interactive REPL:

```bash
./mini-coding-agent-go
```

Run a single prompt without entering the REPL:

```bash
./mini-coding-agent-go prompt "Inspect this repo and summarize the layout"
```

(`prompt` has the alias `p`.)

By default it uses:

- model: `gemma4:cloud`
- approval: `ask`

For a concrete usage example, see [EXAMPLE.md](EXAMPLE.md).

&nbsp;
## Approval Modes

Risky tools such as shell commands and file writes are gated by approval.

- `--approval ask`
  prompts before risky actions (default and recommended)
- `--approval auto`
  allows risky actions automatically, including arbitrary command execution and file writes by the model; use only with trusted prompts and trusted repositories
- `--approval never`
  denies risky actions

Example:

```bash
./mini-coding-agent-go --approval auto
```

&nbsp;
## Resume Sessions

The agent saves sessions under the target workspace root in:

```text
.mini-coding-agent/sessions/
```

Resume the latest session:

```bash
./mini-coding-agent-go --resume latest
```

Resume a specific session:

```bash
./mini-coding-agent-go --resume 20260401-144025-2dd0aa
```

&nbsp;
## Interactive Commands

Inside the REPL, slash commands are handled directly by the agent instead of
being sent to the model as a normal task.

- `/help`
  shows the list of available interactive commands
- `/memory`
  prints the distilled session memory, including the current task, tracked files, and notes
- `/session`
  prints the path to the current saved session JSON file
- `/reset`
  clears the current session history and distilled memory but keeps you in the REPL
- `/exit`
  exits the interactive session
- `/quit`
  exits the interactive session; alias for `/exit`

&nbsp;
## Main CLI Flags

```bash
./mini-coding-agent-go --help
```

CLI flags are passed before the agent starts. Use them to choose the workspace,
model connection, resume behavior, approval mode, and generation limits.

Important flags:

- `--cwd`
  sets the workspace directory the agent should inspect and modify; default: `.` (current directory)
- `--model`
  selects the Ollama model name; default: `gemma4:cloud`
- `--host`
  points the agent at the Ollama server URL (usually not needed); default: `http://127.0.0.1:11434`
- `--ollama-timeout`
  controls how long the client waits for an Ollama response (usually not needed); default: `300` seconds
- `--resume`
  resumes a saved session by id or uses `latest`; default: start a new session
- `--approval`
  controls how risky tools are handled: `ask`, `auto`, or `never`; default: `ask`
- `--max-steps`
  limits how many model and tool turns are allowed for one user request; default: `6`
- `--max-new-tokens`
  caps the model output length for each step; default: `4096`
- `--temperature`
  controls sampling randomness; default: `0.2`
- `--top-p`
  controls nucleus sampling for generation; default: `0.9`

&nbsp;
## Example

See [EXAMPLE.md](EXAMPLE.md)

&nbsp;
## Notes & Tips

- The agent expects the model to emit either `<tool>...</tool>` or `<final>...</final>`.
- Different Ollama models will follow those instructions with different reliability.
- If the model does not follow the format well, use a stronger instruction-following model.
- The agent is intentionally small and optimized for readability, not robustness.
- Debug traffic (prompts and raw responses) is appended to `.mini-coding-agent/agent.log`
  under the workspace root.

&nbsp;
## License

The code in this repository is licensed under the [MIT License](LICENSE).

It is a Go port of [rasbt/mini-coding-agent](https://github.com/rasbt/mini-coding-agent),
which is licensed under Apache 2.0; that license is retained in
[LICENSE-APACHE](LICENSE-APACHE) for the portions derived from the original.

&nbsp;
## Credits

- Original Python implementation and design write-up:
  [Sebastian Raschka — Components of a Coding Agent](https://magazine.sebastianraschka.com/p/components-of-a-coding-agent)
- Original repo: [rasbt/mini-coding-agent](https://github.com/rasbt/mini-coding-agent)
