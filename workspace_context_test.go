package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// TestWorkspaceContext_git exercises WorkspaceContext.git against a throwaway repo created
// in t.TempDir(): a fresh `git init -b main` with no commits, one untracked Hello.txt, then
// a first commit. An earlier version ran these subtests against the live workspace
// ~/data/temp/coding-agent — the agent's default --cwd, whose contents drift — so its
// snapshot assertions rotted once real commits landed there; the fixture makes the state
// under test explicit and hermetic.
//
// Subtests run in declaration order and share the fixture: the log subtests observe the
// repo before and after "log returns commits" commits the Hello.txt written in setup.
func TestWorkspaceContext_git(t *testing.T) {
	dir := t.TempDir()
	// Fixture setup uses check=True semantics (fail unless git exits 0), unlike the git()
	// method under test, which falls back.
	runGit(t, dir, "init", "-b", "main")
	if err := afero.WriteFile(afero.NewOsFs(), filepath.Join(dir, "Hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write Hello.txt: %v", err)
	}
	ctx := NewWorkspaceContext(dir)

	t.Run("rev-parse show-toplevel", func(t *testing.T) {
		out, err := ctx.git([]string{"rev-parse", "--show-toplevel"}, "")
		assertNoErr(t, err)
		// NewWorkspaceContext resolves symlinks in Cwd (resolvePath), and git prints the
		// real path too, so the two agree even though t.TempDir() may sit behind a
		// symlink (macOS /var -> /private/var).
		if got := strings.TrimSpace(out); got != ctx.Cwd {
			t.Errorf("toplevel = %q, want %q", got, ctx.Cwd)
		}
	})

	t.Run("branch show-current", func(t *testing.T) {
		out, err := ctx.git([]string{"branch", "--show-current"}, "")
		assertNoErr(t, err)
		// Works on the unborn branch too: git reads HEAD's symbolic ref.
		if got := strings.TrimSpace(out); got != "main" {
			t.Errorf("branch = %q, want %q", got, "main")
		}
	})

	t.Run("status short lists the untracked file", func(t *testing.T) {
		out, err := ctx.git([]string{"status", "--short"}, "")
		assertNoErr(t, err)
		if !strings.Contains(out, "Hello.txt") {
			t.Errorf("status = %q, want it to contain Hello.txt", out)
		}
	})

	t.Run("log on commit-less repo falls back on non-zero exit", func(t *testing.T) {
		// No commits yet: `git log` exits 128. git() treats a non-zero exit like Python's
		// check=True and returns the fallback.
		out, err := ctx.git([]string{"log", "--oneline", "-5"}, "FALLBACK")
		assertNoErr(t, err)
		if out != "FALLBACK" {
			t.Errorf("log = %q, want FALLBACK", out)
		}
	})

	t.Run("log returns commits once they exist", func(t *testing.T) {
		// Inline identity + no signing: the commit must not depend on the machine's
		// global git config (user.name missing, commit.gpgsign set without a key, ...).
		runGit(t, dir, "add", "Hello.txt")
		runGit(t, dir,
			"-c", "user.name=Test User", "-c", "user.email=test@example.com",
			"-c", "commit.gpgsign=false",
			"commit", "-m", "add hello.txt",
		)
		out, err := ctx.git([]string{"log", "--oneline", "-5"}, "FALLBACK")
		assertNoErr(t, err)
		if !strings.Contains(out, "add hello.txt") {
			t.Errorf("log = %q, want it to contain %q", out, "add hello.txt")
		}
	})

	t.Run("fallback surfaces on a hard failure", func(t *testing.T) {
		// A non-existent cwd makes git fail to start (chdir error) -> RunProcess returns a
		// non-nil error -> git() returns the fallback.
		bad := NewWorkspaceContext(filepath.Join(dir, "does-not-exist-xyz"))
		out, err := bad.git([]string{"rev-parse", "--show-toplevel"}, "FALLBACK")
		assertNoErr(t, err)
		if out != "FALLBACK" {
			t.Errorf("got %q, want FALLBACK", out)
		}
	})
}

// runGit runs git in dir for fixture setup and fails the test unless it exits 0 —
// check=True semantics, unlike the WorkspaceContext.git method under test, which falls
// back instead of failing.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	res, err := RunProcess(context.Background(), "git", args, dir, 0)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("git %s (in %s): exit=%d err=%v\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), dir, res.ExitCode, err, res.Stdout, res.Stderr)
	}
}

func assertNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
