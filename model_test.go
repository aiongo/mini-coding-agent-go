package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// CLI default values mirrored from cli.go's flag definitions, so this test constructs the
// OllamaModelClient exactly the way the app does when no flags are passed.
const (
	cliDefaultModel        = "gemma4:cloud"
	cliDefaultHost         = "http://127.0.0.1:11434"
	cliDefaultTemperature  = 0.2
	cliDefaultTopP         = 0.9
	cliDefaultTimeout      = 300
	cliDefaultMaxNewTokens = 4096
)

func TestOllamaModelClient_FromCLIDefaults(t *testing.T) {
	client := NewOllamaModelClient(
		cliDefaultModel, cliDefaultHost,
		cliDefaultTemperature, cliDefaultTopP, cliDefaultTimeout,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	t.Run("construction", func(t *testing.T) {
		oc, ok := client.(*OllamaModelClient)
		if !ok {
			t.Fatalf("NewOllamaModelClient returned %T, want *OllamaModelClient", client)
		}
		if oc.Model != cliDefaultModel {
			t.Errorf("Model = %q, want %q", oc.Model, cliDefaultModel)
		}
		if oc.Host != cliDefaultHost {
			t.Errorf("Host = %q, want %q", oc.Host, cliDefaultHost)
		}
		if oc.Temperature != cliDefaultTemperature {
			t.Errorf("Temperature = %v, want %v", oc.Temperature, cliDefaultTemperature)
		}
		if oc.TopP != cliDefaultTopP {
			t.Errorf("TopP = %v, want %v", oc.TopP, cliDefaultTopP)
		}
		if oc.Timeout != cliDefaultTimeout {
			t.Errorf("Timeout = %v, want %v", oc.Timeout, cliDefaultTimeout)
		}
		if oc.restyClient == nil {
			t.Error("restyClient was not initialized")
		}
	})

	t.Run("complete", func(t *testing.T) {
		// End-to-end against the local Ollama at the CLI default host. Skips when Ollama
		// isn't running so this doesn't fail on machines without it.
		resp, err := client.Complete(context.Background(), "Reply with exactly: hello", cliDefaultMaxNewTokens)
		if err != nil {
			if strings.Contains(err.Error(), "Could not reach Ollama") {
				t.Skipf("ollama not reachable at %s: %v", cliDefaultHost, err)
			}
			t.Fatalf("Complete failed: %v", err)
		}
		if strings.TrimSpace(resp) == "" {
			t.Fatal("Complete returned an empty response")
		}
		t.Logf("ollama response: %q", resp)
	})
}
