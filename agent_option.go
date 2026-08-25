package main

import "log/slog"

// MiniAgentOption configures the optional fields of a MiniAgent. Only the session store is a
// direct argument to NewMiniAgent; the workspace and model client are synthesized inside
// NewMiniAgent from the flag-derived config. Every parameter that has a Python default is a
// With option.
type MiniAgentOption func(*MiniAgent)

// WithApprovalPolicy sets the approval policy for risky tools ("ask" | "auto" | "never").
func WithApprovalPolicy(policy string) MiniAgentOption {
	return func(a *MiniAgent) { a.ApprovalPolicy = policy }
}

// WithMaxSteps sets the maximum number of tool/model turns per request.
func WithMaxSteps(n int) MiniAgentOption {
	return func(a *MiniAgent) { a.MaxSteps = n }
}

// WithMaxNewTokens caps the model output length per step.
func WithMaxNewTokens(n int) MiniAgentOption {
	return func(a *MiniAgent) { a.MaxNewTokens = n }
}

// WithDepth sets this agent's delegation depth (0 for a top-level agent).
func WithDepth(n int) MiniAgentOption {
	return func(a *MiniAgent) { a.Depth = n }
}

// WithMaxDepth sets the delegation depth ceiling.
func WithMaxDepth(n int) MiniAgentOption {
	return func(a *MiniAgent) { a.MaxDepth = n }
}

// WithReadOnly marks the agent read-only (delegated child agents are always read-only).
func WithReadOnly(ro bool) MiniAgentOption {
	return func(a *MiniAgent) { a.ReadOnly = ro }
}

// WithSession supplies a pre-existing session (e.g. one resumed from disk) instead of
// creating a fresh one.
func WithSession(s *Session) MiniAgentOption {
	return func(a *MiniAgent) { a.Session = s }
}

// WithCwd sets the workspace directory; NewMiniAgent synthesizes the WorkspaceContext from it.
func WithCwd(s string) MiniAgentOption {
	return func(a *MiniAgent) { a.Cwd = s }
}

// WithModel sets the Ollama model name; NewMiniAgent synthesizes the ModelClient with it.
func WithModel(s string) MiniAgentOption {
	return func(a *MiniAgent) { a.Model = s }
}

// WithHost sets the Ollama server URL for the synthesized ModelClient.
func WithHost(s string) MiniAgentOption {
	return func(a *MiniAgent) { a.Host = s }
}

// WithTemperature sets the sampling temperature for the synthesized ModelClient.
func WithTemperature(v float64) MiniAgentOption {
	return func(a *MiniAgent) { a.Temperature = v }
}

// WithTopP sets the top-p sampling value for the synthesized ModelClient.
func WithTopP(v float64) MiniAgentOption {
	return func(a *MiniAgent) { a.TopP = v }
}

// WithOllamaTimeout sets the Ollama request timeout (seconds) for the synthesized ModelClient.
func WithOllamaTimeout(n int) MiniAgentOption {
	return func(a *MiniAgent) { a.OllamaTimeout = n }
}

// WithResume sets the session id (or "latest") to resume; NewMiniAgent resolves it into a Session.
func WithResume(s string) MiniAgentOption {
	return func(a *MiniAgent) { a.Resume = s }
}

// WithLogger injects a *slog.Logger into the agent; NewMiniAgent also forwards it to the
// synthesized ModelClient so request/response traffic is logged at debug. Defaults to
// NewLogger() (debug level, file only — .mini-coding-agent/agent.log under the repo root) when not set.
func WithLogger(l *slog.Logger) MiniAgentOption {
	return func(a *MiniAgent) { a.Logger = l }
}
