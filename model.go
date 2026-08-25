package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"resty.dev/v3"
)

// ModelClient is the abstraction Python reaches via duck typing: any object exposing a
// complete(prompt, max_new_tokens) method (e.g. OllamaModelClient, FakeModelClient in
// mini_coding_agent.py L167-222).
//
// Go-idiomatic deviation: Complete takes a context.Context so the Ollama client can apply
// its timeout/cancellation. Python instead stores the timeout inside the client and calls
// complete(prompt, max_new_tokens) with no per-call cancellation.
type ModelClient interface {
	Complete(ctx context.Context, prompt string, maxNewTokens int) (string, error)
}

// OllamaModelClient mirrors Python OllamaModelClient (mini_coding_agent.py L179-222): it
// POSTs to Ollama's /api/generate completion endpoint via resty.
type OllamaModelClient struct {
	Model       string  // model
	Host        string  // host (trailing "/" trimmed, like Python host.rstrip("/"))
	Temperature float64 // temperature
	TopP        float64 // top_p
	Timeout     int     // timeout (seconds)
	restyClient *resty.Client
	logger      *slog.Logger
}

// NewOllamaModelClient mirrors Python OllamaModelClient.__init__(model, host, temperature,
// top_p, timeout). The logger is injected (not in the Python original) so Complete can log the
// request/response at debug; a nil logger falls back to a no-op so the client never panics.
func NewOllamaModelClient(model, host string, temperature, topP float64, timeout int, logger *slog.Logger) ModelClient {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &OllamaModelClient{
		Model:       model,
		Host:        strings.TrimRight(host, "/"),
		Temperature: temperature,
		TopP:        topP,
		Timeout:     timeout,
		restyClient: resty.New(),
		logger:      logger,
	}
}

// ollamaResponse is the JSON body returned by Ollama's /api/generate (stream=false).
// Python reads data.get("response", "") and data.get("error").
type ollamaResponse struct {
	Response string `json:"response"`
	Error    string `json:"error"`
}

// Complete mirrors Python OllamaModelClient.complete (mini_coding_agent.py L187-222): POST
// the generate payload to <host>/api/generate and return the model's response text. The
// client's Timeout (seconds) is applied via the context on top of the caller's ctx.
func (c *OllamaModelClient) Complete(ctx context.Context, prompt string, maxNewTokens int) (string, error) {
	payload := map[string]any{
		"model":  c.Model,
		"prompt": prompt,
		"stream": false,
		"raw":    false,
		"think":  false,
		"options": map[string]any{
			"num_predict": maxNewTokens,
			"temperature": c.Temperature,
			"top_p":       c.TopP,
		},
	}

	c.logger.Debug("ollama generate request",
		slog.String("model", c.Model),
		slog.String("host", c.Host),
		slog.Int("max_new_tokens", maxNewTokens),
		slog.Float64("temperature", c.Temperature),
		slog.Float64("top_p", c.TopP),
		slog.String("prompt", prompt),
	)

	resp, err := c.restyClient.R().
		SetTimeout(time.Duration(c.Timeout) * time.Second).
		SetContext(ctx).
		SetBody(payload).
		Post(c.Host + "/api/generate")
	if err != nil {
		// transport / connection / timeout failure (Python: URLError)
		return "", fmt.Errorf(
			"Could not reach Ollama.\nMake sure `ollama serve` is running and the model is available.\nHost: %s\nModel: %s",
			c.Host, c.Model,
		)
	}
	if resp.StatusCode() >= 400 {
		// Python: HTTPError -> "Ollama request failed with HTTP {code}: {body}"
		return "", fmt.Errorf("Ollama request failed with HTTP %d: %s", resp.StatusCode(), resp.String())
	}

	var data ollamaResponse
	if err := json.Unmarshal(resp.Bytes(), &data); err != nil {
		return "", fmt.Errorf("Ollama returned malformed JSON: %w", err)
	}
	if data.Error != "" {
		return "", fmt.Errorf("Ollama error: %s", data.Error)
	}
	c.logger.Debug("ollama generate response", slog.String("response", data.Response))
	return data.Response, nil
}
