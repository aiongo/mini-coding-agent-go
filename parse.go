package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// This file ports Python's response-parsing layer (mini_coding_agent.py L615-715): the model's
// free-form text is reduced to one of three outcomes — a tool call (JSON or XML form), a
// final answer, or a "retry" notice when the output is malformed. The ask loop (mini_agent.go)
// drives on ParseResult.Kind.

// ParseResult is the outcome of Parse. Exactly one field is meaningful depending on Kind:
//   - "tool"   -> Tool is {"name": string, "args": map[string]any}
//   - "final"  -> Final is the answer text
//   - "retry"  -> Notice is the corrective runtime notice fed back to the model
type ParseResult struct {
	Kind   string         // "tool" | "final" | "retry"
	Tool   map[string]any // Kind == "tool"
	Final  string         // Kind == "final"
	Notice string         // Kind == "retry"
}

// Parse mirrors Python MiniAgent.parse (mini_coding_agent.py L616-647). Precedence mirrors
// Python exactly: a JSON-style <tool>{...}</tool> wins over an XML-style <tool name=.. ..>,
// and a <tool>/<tool form wins over <final> only when it appears earlier in the text.
func Parse(raw string) ParseResult {
	if strings.Contains(raw, "<tool>") && toolBeforeFinal(raw, "<tool>") {
		body := extract(raw, "tool")
		// Decode into any first so a valid-but-non-object body (e.g. `[1,2]` or `"x"`) can be
		// distinguished from malformed JSON — matching Python's "tool payload must be a JSON
		// object" branch. json.Unmarshal straight into map[string]any would conflate the two.
		var generic any
		if err := json.Unmarshal([]byte(body), &generic); err != nil {
			return ParseResult{Kind: "retry", Notice: retryNotice("model returned malformed tool JSON")}
		}
		payload, ok := generic.(map[string]any)
		if !ok {
			return ParseResult{Kind: "retry", Notice: retryNotice("tool payload must be a JSON object")}
		}
		name, _ := payload["name"].(string)
		if strings.TrimSpace(name) == "" {
			return ParseResult{Kind: "retry", Notice: retryNotice("tool payload is missing a tool name")}
		}
		args, hasArgs := payload["args"]
		if !hasArgs || args == nil {
			payload["args"] = map[string]any{}
		} else if _, isMap := args.(map[string]any); !isMap {
			return ParseResult{Kind: "retry", Notice: retryNotice("")}
		}
		return ParseResult{Kind: "tool", Tool: payload}
	}
	if strings.Contains(raw, "<tool") && toolBeforeFinal(raw, "<tool") {
		payload, ok := parseXMLTool(raw)
		if ok {
			return ParseResult{Kind: "tool", Tool: payload}
		}
		return ParseResult{Kind: "retry", Notice: retryNotice("")}
	}
	if strings.Contains(raw, "<final>") {
		final := strings.TrimSpace(extract(raw, "final"))
		if final != "" {
			return ParseResult{Kind: "final", Final: final}
		}
		return ParseResult{Kind: "retry", Notice: retryNotice("model returned an empty <final> answer")}
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" {
		return ParseResult{Kind: "final", Final: trimmed}
	}
	return ParseResult{Kind: "retry", Notice: retryNotice("model returned an empty response")}
}

// toolBeforeFinal mirrors Python's `<final>` guard: true when there is no <final>, or when the
// needle (<tool> / <tool) occurs before the first <final>. Caller guarantees the needle exists.
func toolBeforeFinal(raw, needle string) bool {
	if !strings.Contains(raw, "<final>") {
		return true
	}
	return strings.Index(raw, needle) < strings.Index(raw, "<final>")
}

// retryNotice mirrors Python MiniAgent.retry_notice (mini_coding_agent.py L650-659). An empty
// problem maps to Python's default "model returned malformed tool output".
func retryNotice(problem string) string {
	prefix := "Runtime notice"
	if problem != "" {
		prefix += ": " + problem
	} else {
		prefix += ": model returned malformed tool output"
	}
	return prefix + ". Reply with a valid <tool> call or a non-empty <final> answer. " +
		`For multi-line files, prefer <tool name="write_file" path="file.py"><content>...</content></tool>.`
}

// xmlToolRe mirrors Python parse_xml_tool's regex with re.S (dotall): the attrs segment is
// everything up to the closing > of the opening <tool ...> tag, the body is non-greedy up to
// </tool>. (?s) makes . match newlines so multi-line <content> bodies are captured whole.
var xmlToolRe = regexp.MustCompile(`(?s)<tool([^>]*)>(.*?)</tool>`)

// parseXMLTool mirrors Python MiniAgent.parse_xml_tool (mini_coding_agent.py L662-682): parse a
// <tool name=".." path=".."><content>..</content></tool> form. Attributes (except name) become
// args; known text-bearing child tags (content/old_text/new_text/command/task/pattern/path) are
// lifted out of the body verbatim (no escaping, via extractRaw). write_file/delegate fall back
// to the stripped body when their text tag is absent. Returns (_, false) when no match or no name.
func parseXMLTool(raw string) (map[string]any, bool) {
	m := xmlToolRe.FindStringSubmatch(raw)
	if m == nil {
		return nil, false
	}
	attrs := parseAttrs(m[1])
	name := strings.TrimSpace(attrs["name"])
	if name == "" {
		return nil, false
	}
	delete(attrs, "name")

	body := m[2]
	args := make(map[string]any, len(attrs)+1)
	for k, v := range attrs {
		args[k] = v
	}
	for _, key := range []string{"content", "old_text", "new_text", "command", "task", "pattern", "path"} {
		if strings.Contains(body, "<"+key+">") {
			args[key] = extractRaw(body, key)
		}
	}
	bodyText := strings.Trim(body, "\n")
	if name == "write_file" {
		if _, ok := args["content"]; !ok && bodyText != "" {
			args["content"] = bodyText
		}
	}
	if name == "delegate" {
		if _, ok := args["task"]; !ok && bodyText != "" {
			args["task"] = strings.TrimSpace(bodyText)
		}
	}
	return map[string]any{"name": name, "args": args}, true
}

// attrsRe mirrors Python parse_attrs (mini_coding_agent.py L685-689): key="..." or key='...'.
var attrsRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// parseAttrs extracts the attribute map from an opening-tag's attribute text.
func parseAttrs(text string) map[string]string {
	attrs := map[string]string{}
	for _, m := range attrsRe.FindAllStringSubmatch(text, -1) {
		// m[2] is the double-quoted capture, m[3] the single-quoted one; exactly one matched.
		if m[2] != "" {
			attrs[m[1]] = m[2]
		} else {
			attrs[m[1]] = m[3]
		}
	}
	return attrs
}

// extract mirrors Python MiniAgent.extract (mini_coding_agent.py L692-702): the content of the
// first <tag>...</tag>, whitespace-trimmed. If the start tag is absent the whole text is returned
// (matching Python); a missing end tag returns the trimmed tail.
func extract(text, tag string) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	start := strings.Index(text, startTag)
	if start == -1 {
		return text
	}
	start += len(startTag)
	end := strings.Index(text[start:], endTag)
	if end == -1 {
		return strings.TrimSpace(text[start:])
	}
	return strings.TrimSpace(text[start : start+end])
}

// extractRaw mirrors Python MiniAgent.extract_raw (mini_coding_agent.py L704-715): identical to
// extract but WITHOUT trimming — the captured text (e.g. file content) is preserved verbatim.
func extractRaw(text, tag string) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	start := strings.Index(text, startTag)
	if start == -1 {
		return text
	}
	start += len(startTag)
	end := strings.Index(text[start:], endTag)
	if end == -1 {
		return text[start:]
	}
	return text[start : start+end]
}
