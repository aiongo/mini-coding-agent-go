package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/afero"
)

// SessionStore mirrors Python SessionStore (mini_coding_agent.py L146-164): a JSON
// session store under Root that supports save, load, and latest (newest by mtime).
// File access goes through afero.
type SessionStore struct {
	Root string
	fs   afero.Fs
}

// NewSessionStore mirrors Python SessionStore.__init__: it records root and creates it
// (mkdir -p, exist_ok). afero backs the filesystem with an in-memory-tested OsFs by default.
func NewSessionStore(root string) *SessionStore {
	fs := afero.NewOsFs()
	_ = fs.MkdirAll(root, 0o755) // best-effort mkdir -p; Save surfaces any real failure
	return &SessionStore{Root: root, fs: fs}
}

// path returns the session file for sessionID: Root/<id>.json.
func (s *SessionStore) path(sessionID string) string {
	return filepath.Join(s.Root, sessionID+".json")
}

// Save mirrors Python SessionStore.save: writes the session as indented JSON to
// Root/<id>.json (json.dumps(indent=2)) and returns the path.
func (s *SessionStore) Save(session *Session) (string, error) {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return "", err
	}
	p := s.path(session.ID)
	if err := afero.WriteFile(s.fs, p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// Load mirrors Python SessionStore.load: reads and unmarshals Root/<id>.json.
func (s *SessionStore) Load(sessionID string) (*Session, error) {
	data, err := afero.ReadFile(s.fs, s.path(sessionID))
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// Latest mirrors Python SessionStore.latest: the newest *.json by mtime, returning its
// id (filename without ".json"), or "" if there are none.
func (s *SessionStore) Latest() (string, error) {
	matches, err := afero.Glob(s.fs, filepath.Join(s.Root, "*.json"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	slices.SortFunc(matches, func(a, b string) int {
		return s.modTime(a).Compare(s.modTime(b))
	})
	last := matches[len(matches)-1]
	return strings.TrimSuffix(filepath.Base(last), ".json"), nil
}

// modTime returns the file mtime, or the zero time if Stat fails.
func (s *SessionStore) modTime(name string) time.Time {
	if info, err := s.fs.Stat(name); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}
