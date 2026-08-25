package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Session mirrors the session dict Python creates/loads in MiniAgent.__init__
// (mini_coding_agent.py L249-255) and persists via SessionStore. JSON tags match the
// Python dict keys so session files stay interchangeable in spirit.
type Session struct {
	ID            string        `json:"id"`
	CreatedAt     string        `json:"created_at"`
	WorkspaceRoot string        `json:"workspace_root"`
	History       []HistoryItem `json:"history"`
	Memory        Memory        `json:"memory"`
}

// Memory is the distilled working memory (mini_coding_agent.py L254):
// {"task": "", "files": [], "notes": []}.
type Memory struct {
	Task  string   `json:"task"`
	Files []string `json:"files"`
	Notes []string `json:"notes"`
}

// HistoryItem is one transcript entry. User/assistant rows use Role+Content+CreatedAt;
// tool rows additionally set Name+Args (mini_coding_agent.py record()/ask()).
type HistoryItem struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Name      string         `json:"name,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	CreatedAt string         `json:"created_at"`
}

// newSession builds the default session Python creates when none is supplied
// (mini_coding_agent.py L249-255). Slices are non-nil so they marshal as [] not null.
func newSession(workspaceRoot string) *Session {
	return &Session{
		ID:            newSessionID(),
		CreatedAt:     Now(), // Python now() -> UTC ISO8601
		WorkspaceRoot: workspaceRoot,
		History:       []HistoryItem{},
		Memory:        Memory{Task: "", Files: []string{}, Notes: []string{}},
	}
}

// newSessionID mirrors Python:
//
//	datetime.now().strftime("%Y%m%d-%H%M%S") + "-" + uuid.uuid4().hex[:6]
//
// The date stamp is naive local time (matches Python datetime.now()), distinct from
// Now() which is UTC. uuid4().hex[:6] is 6 random hex chars -> 3 random bytes.
func newSessionID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand essentially never fails here; keep a stable fallback so agent
		// construction never errors on session id generation.
		b = []byte{0, 0, 0}
	}
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
