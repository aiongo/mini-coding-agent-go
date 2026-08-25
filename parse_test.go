package main

import (
	"strings"
	"testing"
)

// TestParse pins the response-parsing precedence and shape against Python MiniAgent.parse
// (mini_coding_agent.py L616-647): JSON <tool> beats XML <tool>; a <tool>/<tool form beats
// <final> only when it appears first; bare non-empty text is a final; empty/malformed is retry.
func TestParse(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		kind     string
		toolName string
		final    string
	}{
		{"json tool", `<tool>{"name":"list_files","args":{"path":"."}}</tool>`, "tool", "list_files", ""},
		{"xml tool", `<tool name="write_file" path="a.py"><content>hi</content></tool>`, "tool", "write_file", ""},
		{"final tag", `<final>done</final>`, "final", "", "done"},
		{"bare text", "hello world", "final", "", "hello world"},
		{"empty", "", "retry", "", ""},
		{"malformed json", `<tool>{not json}</tool>`, "retry", "", ""},
		{"missing name", `<tool>{"args":{}}</tool>`, "retry", "", ""},
		{"multi tool takes first", `<tool>{"name":"a","args":{}}</tool> <tool>{"name":"b","args":{}}</tool>`, "tool", "a", ""},
		{"tool before final", `<tool>{"name":"a","args":{}}</tool><final>x</final>`, "tool", "a", ""},
		{"final before tool", `<final>x</final><tool>{"name":"a","args":{}}</tool>`, "final", "", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := Parse(tc.raw)
			if pr.Kind != tc.kind {
				t.Fatalf("kind = %q, want %q", pr.Kind, tc.kind)
			}
			switch pr.Kind {
			case "tool":
				name, _ := pr.Tool["name"].(string)
				if name != tc.toolName {
					t.Fatalf("tool name = %q, want %q", name, tc.toolName)
				}
			case "final":
				if pr.Final != tc.final {
					t.Fatalf("final = %q, want %q", pr.Final, tc.final)
				}
			case "retry":
				if !strings.Contains(pr.Notice, "Runtime notice") {
					t.Fatalf("notice = %q, want 'Runtime notice'", pr.Notice)
				}
			}
		})
	}
}

// TestParseXMLMultilineContent checks the whole point of the XML form: multi-line file content
// is captured verbatim (no JSON escaping of newlines/quotes), via extractRaw.
func TestParseXMLMultilineContent(t *testing.T) {
	raw := `<tool name="write_file" path="binary_search.py"><content>def f():
    return -1
</content></tool>`
	pr := Parse(raw)
	if pr.Kind != "tool" {
		t.Fatalf("kind = %q, want tool", pr.Kind)
	}
	if name, _ := pr.Tool["name"].(string); name != "write_file" {
		t.Fatalf("name = %q", name)
	}
	args, _ := pr.Tool["args"].(map[string]any)
	if path, _ := args["path"].(string); path != "binary_search.py" {
		t.Fatalf("path = %q", path)
	}
	content, _ := args["content"].(string)
	if !strings.Contains(content, "return -1") || !strings.Contains(content, "\n") {
		t.Fatalf("content = %q, want verbatim multi-line", content)
	}
}

func TestExtractRawPreservesWhitespace(t *testing.T) {
	if got := extractRaw("<content>  a\nb  </content>", "content"); got != "  a\nb  " {
		t.Fatalf("extractRaw = %q", got)
	}
}

func TestRetryNotice(t *testing.T) {
	if !strings.Contains(retryNotice("boom"), "Runtime notice: boom") {
		t.Fatalf("problem not embedded in notice")
	}
	if !strings.Contains(retryNotice(""), "model returned malformed tool output") {
		t.Fatalf("default notice missing")
	}
}
