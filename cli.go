package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/urfave/cli/v3"
)

// Cli wraps the urfave/cli command tree: the root command (interactive REPL by default,
// all flags attached) plus the prompt subcommand for one-shot invocations.
type Cli struct {
	cmd *cli.Command

	// ran is set when a real command action executed (vs. help/version/no-op).
	// Currently write-only: reserved for a caller that needs to know whether the
	// invocation actually did work.
	ran atomic.Bool
}

// NewCli builds the Cli and initializes its command tree; call Run to execute it.
func NewCli() *Cli {
	c := &Cli{}
	c.init()
	return c
}

func (c *Cli) init() {
	// parser.add_argument("prompt", nargs="*", help="Optional one-shot prompt.")
	// parser.add_argument("--cwd", default=".", help="Workspace directory.")
	// parser.add_argument("--model", default="qwen3.5:4b", help="Ollama model name.")
	// parser.add_argument("--host", default="http://127.0.0.1:11434", help="Ollama server URL.")
	// parser.add_argument("--ollama-timeout", type=int, default=300, help="Ollama request timeout in seconds.")
	// parser.add_argument("--resume", default=None, help="Session id to resume or 'latest'.")
	// parser.add_argument(
	//     "--approval",
	//     choices=("ask", "auto", "never"),
	//     default="ask",
	//     help="Approval policy for risky tools; auto grants the model arbitrary command execution and file writes.",
	// )
	// parser.add_argument("--max-steps", type=int, default=6, help="Maximum tool/model iterations per request.")
	// parser.add_argument("--max-new-tokens", type=int, default=512, help="Maximum model output tokens per step.")
	// parser.add_argument("--temperature", type=float, default=0.2, help="Sampling temperature sent to Ollama.")
	// parser.add_argument("--top-p", type=float, default=0.9, help="Top-p sampling value sent to Ollama.")
	c.cmd = &cli.Command{
		Name:    "mini-coding-agent-go",
		Usage:   "Minimal coding agent for Ollama models.",
		Version: "1.0",
		// Root action = interactive REPL (Python main's no-prompt branch). The one-shot path
		// is the `prompt` subcommand below; both ultimately call agent.Ask.
		Action: c.ranAction(c.handleInteract),
		// Flags live on the root command so BOTH entry points share them: the root
		// action (Serve/REPL) reads them directly, and the `prompt` subcommand inherits
		// them via urfave/cli v3 persistent-flag propagation — lookupFlag walks Lineage()
		// upward, and FlagBase.Local defaults to false, so root flags auto-apply to
		// subcommands. Mirrors Python argparse, where these are all root-level flags.
		// (The previous placement on the `prompt` subcommand left the root path reading
		// "" for every flag, since the root's Lineage is just itself.)
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "cwd",
				Value: ".",
				Usage: "Workspace directory.",
			},
			&cli.StringFlag{
				Name:  "model",
				Value: "gemma4:cloud",
				Usage: "Ollama model name.",
			},
			&cli.StringFlag{
				Name:  "host",
				Value: "http://127.0.0.1:11434",
				Usage: "Ollama server URL.",
			},
			&cli.IntFlag{
				Name:  "ollama-timeout",
				Value: 300,
				Usage: "Ollama request timeout in seconds.",
			},
			&cli.StringFlag{
				Name:  "resume",
				Value: "", // empty string means unset (Python None)
				Usage: "Session id to resume or 'latest'.",
			},
			&cli.StringFlag{
				Name:  "approval",
				Value: "ask",
				// ValidValues: []string{"ask", "auto", "never"},
				Validator: func(s string) error {
					values := []string{"ask", "auto", "never"}
					if slices.Contains(values, s) {
						return nil
					}

					return fmt.Errorf("invalid approval value: %s, must be one of ask, auto, never", s)
				},
				Usage: "Approval policy for risky tools; auto grants the model arbitrary command execution and file writes.",
			},
			&cli.IntFlag{
				Name:  "max-steps",
				Value: 6,
				Usage: "Maximum tool/model iterations per request.",
			},
			&cli.IntFlag{
				Name: "max-new-tokens",
				// Python default is 512 (see the argparse block above), but that is too small to
				// emit a whole file inside one <tool name="write_file">...<content>...</content></tool>
				// call — the output is truncated before the closing tags and parse() rejects it as
				// malformed every turn, looping until max_attempts (see session 20260717-022051).
				// 4096 leaves room for full file writes; intentional deviation from the Python default.
				Value: 4096,
				Usage: "Maximum model output tokens per step.",
			},
			&cli.FloatFlag{
				Name:  "temperature",
				Value: 0.2,
				Usage: "Sampling temperature sent to Ollama.",
			},
			&cli.FloatFlag{
				Name:  "top-p",
				Value: 0.9,
				Usage: "Top-p sampling value sent to Ollama.",
			},
		},
		Commands: []*cli.Command{
			{
				Name:    "prompt",
				Aliases: []string{"p"},
				Usage:   "One-shot prompt to send to the model.(Interactive Mode to input *)",
				Action:  c.ranAction(c.handleServe),
				// Surface arg/usage errors (e.g. missing prompt) the same way
				// urfave surfaces missing required flags: error + help, nonzero exit.
				OnUsageError: c.onUsageError,
				// prompt is required: Min=1 makes urfave/cli reject an empty
				// invocation (matching the Python argparse nargs="*" but non-empty).
				// Max=-1 allows multi-word prompts passed as separate args.
				Arguments: []cli.Argument{
					&cli.StringArgs{
						Name:  "prompt",
						Min:   1,
						Max:   -1,
						Value: "*",
					},
				},
			},
		},
	}
}

func (c *Cli) ranAction(action cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		c.ran.Store(true)
		return action(ctx, cmd)
	}
}

func (c *Cli) handleServe(ctx context.Context, cmd *cli.Command) error {
	agent := NewMiniAgent(miniAgentOptionsFromFlags(cmd)...)
	fmt.Println(agent.buildWelcome())
	// "prompt" is a positional Argument (cli.StringArgs), not a flag — cmd.String only resolves
	// flags. StringArgs (the slice variant) does not surface its default Value, so a theoretically
	// empty read falls back to "*"; in practice Min=1 on the argument rejects an empty invocation.
	parts := cmd.StringArgs("prompt")
	userMessage := strings.Join(parts, " ")
	if userMessage == "" {
		userMessage = "*"
	}
	fmt.Println()
	final, err := agent.Ask(userMessage)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	fmt.Println(final)
	return nil
}

// handleInteract is the REPL path (root action), mirroring Python main's while-True loop
// (mini_coding_agent.py L986-1010): read a line, dispatch slash-commands (/help /memory /session
// /reset /exit), otherwise run one Ask and print its final answer. EOF exits cleanly. Both this
// and handleServe use agent.Ask as the atomic per-request handler.
func (c *Cli) handleInteract(ctx context.Context, cmd *cli.Command) error {
	agent := NewMiniAgent(miniAgentOptionsFromFlags(cmd)...)
	fmt.Println(agent.buildWelcome())
	for {
		fmt.Print("\nmini-coding-agent> ")
		line, err := readStdinLine()
		if err != nil {
			// EOF (Ctrl-D) / read error — mirror Python's EOFError branch: blank line, exit 0.
			fmt.Println()
			return nil
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		switch input {
		case "/exit", "/quit":
			return nil
		case "/help":
			fmt.Println(HELP_DETAILS)
			continue
		case "/memory":
			fmt.Println(agent.memoryText())
			continue
		case "/session":
			fmt.Println(agent.SessionPath)
			continue
		case "/reset":
			agent.reset()
			fmt.Println("session reset")
			continue
		}
		fmt.Println()
		final, err := agent.Ask(input)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		fmt.Println(final)
	}
}

// miniAgentOptionsFromFlags reads every CLI flag off cmd and returns the matching
// MiniAgentOption slice — one option per flag, parsed one by one. This is the shared
// flag->options step both the one-shot and REPL paths will call. NewMiniAgent then
// synthesizes the WorkspaceContext (from cwd), the ModelClient (from model/host/
// temperature/top-p/ollama-timeout), and the resumed Session (from resume) out of these.
func miniAgentOptionsFromFlags(cmd *cli.Command) []MiniAgentOption {
	var opts []MiniAgentOption
	opts = append(opts, WithCwd(cmd.String("cwd")))
	opts = append(opts, WithModel(cmd.String("model")))
	opts = append(opts, WithHost(cmd.String("host")))
	opts = append(opts, WithOllamaTimeout(cmd.Int("ollama-timeout")))
	opts = append(opts, WithResume(cmd.String("resume")))
	opts = append(opts, WithApprovalPolicy(cmd.String("approval")))
	opts = append(opts, WithMaxSteps(cmd.Int("max-steps")))
	opts = append(opts, WithMaxNewTokens(cmd.Int("max-new-tokens")))
	opts = append(opts, WithTemperature(cmd.Float("temperature")))
	opts = append(opts, WithTopP(cmd.Float("top-p")))
	return opts
}

// onUsageError prints the usage error (e.g. missing required prompt) together
// with the command help, matching how urfave reports missing required flags.
func (c *Cli) onUsageError(_ context.Context, cmd *cli.Command, err error, _ bool) error {
	fmt.Fprintf(cmd.Root().ErrWriter, "Incorrect Usage: %s\n\n", err.Error())
	_ = cli.ShowSubcommandHelp(cmd)
	return err
}

// Run executes the selected command. It is invoked from the fx lifecycle hook
// in app.go.
func (c *Cli) Run() error {
	return c.cmd.Run(context.Background(), os.Args)
}
