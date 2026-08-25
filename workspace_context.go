package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
)

// WorkspaceContext mirrors Python WorkspaceContext (mini_coding_agent.py L75-140).
// It holds the stable workspace facts collected once at startup (component #1:
// "Live Repo Context").
type WorkspaceContext struct {
	Cwd           string
	RepoRoot      string
	Branch        string
	DefaultBranch string
	Status        string
	RecentCommits []string
	ProjectDocs   map[string]string
}

// NewWorkspaceContext mirrors Python WorkspaceContext.build(cwd) (mini_coding_agent.py
// L86-123): resolves cwd, runs git to gather repo facts (repo root, branch, default branch,
// status, recent commits), then reads the project docs (AGENTS.md, README.md,
// pyproject.toml, package.json) from repo_root and cwd, clipping each to 1200 chars.
// File access goes through afero.
func NewWorkspaceContext(cwd string) *WorkspaceContext {
	// cwd = Path(cwd).resolve()
	cwd = resolvePath(cwd)
	ctx := &WorkspaceContext{Cwd: cwd}

	// git runs in ctx.Cwd and mirrors Python's inner helper: it strips stdout and falls
	// back on a non-zero exit, an error, or an empty result (see the git method below).
	// repo_root = Path(git(["rev-parse", "--show-toplevel"], str(cwd))).resolve()
	repoRootOut, _ := ctx.git([]string{"rev-parse", "--show-toplevel"}, cwd)
	repoRoot := resolvePath(repoRootOut)

	ctx.RepoRoot = repoRoot
	// branch = git([...], "-") or "-"
	ctx.Branch, _ = ctx.git([]string{"branch", "--show-current"}, "-")
	// default_branch = (git([...], "origin/main") or "origin/main").removeprefix("origin/")
	defaultBranch, _ := ctx.git([]string{"symbolic-ref", "--short", "refs/remotes/origin/HEAD"}, "origin/main")
	ctx.DefaultBranch = strings.TrimPrefix(defaultBranch, "origin/")
	// status = clip(git([...], "clean") or "clean", 1500)
	status, _ := ctx.git([]string{"status", "--short"}, "clean")
	ctx.Status = Clip(status, 1500)
	// recent_commits = [line for line in git([...]).splitlines() if line]
	logOut, _ := ctx.git([]string{"log", "--oneline", "-5"}, "")
	for line := range strings.SplitSeq(logOut, "\n") {
		if line != "" {
			ctx.RecentCommits = append(ctx.RecentCommits, line)
		}
	}
	// project_docs = { ... } (read from repo_root then cwd, dedup by repo-root-relative key)
	ctx.ProjectDocs = readProjectDocs(repoRoot, cwd)
	return ctx
}

// readProjectDocs mirrors the docs loop in Python WorkspaceContext.build (L104-113): for
// each base in (repoRoot, cwd), read DOC_NAMES, key by repo-root-relative path, skip dups,
// and clip each to 1200 chars. Uses afero for file access.
func readProjectDocs(repoRoot, cwd string) map[string]string {
	fs := afero.NewOsFs()
	docs := map[string]string{}
	for _, base := range []string{repoRoot, cwd} {
		for _, name := range DOC_NAMES {
			path := filepath.Join(base, name)
			if exists, _ := afero.Exists(fs, path); !exists {
				continue
			}
			key, err := filepath.Rel(repoRoot, path)
			if err != nil {
				key = name
			}
			if _, ok := docs[key]; ok {
				continue
			}
			data, err := afero.ReadFile(fs, path)
			if err != nil {
				continue
			}
			text := strings.ToValidUTF8(string(data), "\uFFFD") // py read_text(errors="replace")
			docs[key] = Clip(text, 1200)
		}
	}
	return docs
}

// resolvePath mirrors Python Path.resolve(): an absolute path with symlinks resolved.
// It first expands a leading "~" (Go's os/path/filepath never do this on their own —
// see expandHome), then makes it absolute and resolves symlinks.
//
// filepath.EvalSymlinks only succeeds when the WHOLE path exists, but Python's non-strict
// resolve also substitutes the target of a dangling (not-yet-existing) symlink. Without
// that, a symlink inside the workspace pointing at a missing outside path keeps its
// in-workspace lexical form here, passes pathIsWithinRoot, and write_file's
// O_CREATE (afero.WriteFile) follows it outside the root — a sandbox escape. So on
// EvalSymlinks failure, resolve the deepest existing ancestor, then walk the remaining
// components following symlinks (dangling targets included; bounded, to survive cycles).
// os.Lstat/os.Readlink are the same afero-has-no-equivalent exception as os.SameFile.
func resolvePath(p string) string {
	p = expandHome(p)
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	// Missing tail: resolve the parent recursively, then follow the remaining links.
	dir, file := filepath.Split(abs)
	cur := resolvePath(filepath.Clean(dir))
	for comp := range strings.SplitSeq(file, "/") {
		if comp == "" {
			continue
		}
		next := filepath.Join(cur, comp)
		for range 40 { // follow symlink chains, bounded against cycles
			info, err := os.Lstat(next)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				break
			}
			target, err := os.Readlink(next)
			if err != nil {
				break
			}
			if filepath.IsAbs(target) {
				next = filepath.Clean(target)
			} else {
				next = filepath.Join(filepath.Dir(next), target)
			}
		}
		cur = next
	}
	return cur
}

// expandHome replaces a leading "~" or "~/" with the current user's home directory,
// mirroring Python's Path.expanduser(). Go's os and path/filepath packages never expand
// "~" on their own — unlike a shell, they treat it as a literal segment — so a flag value
// such as "~/data/temp/coding-agent" must be expanded before filepath.Abs, otherwise it
// becomes "<cwd>/~/data/temp/coding-agent". "~user" (another user's home) is not supported.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return home + p[1:] // keep the leading '/'
}

// git mirrors Python build's inner git helper: run git in ctx.Cwd, strip stdout, and fall
// back on a non-zero exit, an error, or an empty result (Python: subprocess.run with
// check=True, then `result.stdout.strip() or fallback`).
func (ctx *WorkspaceContext) git(args []string, fallback string) (string, error) {
	// Bound startup git calls (10s) so a hung git binary can't block agent construction
	// forever. RunProcess applies timeoutSecs via context.WithTimeout internally.
	cmdResult, err := RunProcess(context.Background(), "git", args, ctx.Cwd, 10)

	if cmdResult.ExitCode != 0 || err != nil {
		return fallback, nil
	}
	// Python: result.stdout.strip() or fallback
	if out := strings.TrimSpace(cmdResult.Stdout); out != "" {
		return out, nil
	}
	return fallback, nil
}

// String mirrors Python WorkspaceContext.text() (mini_coding_agent.py L125-140),
// rendering the workspace snapshot used in the prompt prefix.
//
// Note: Python's project_docs is an insertion-ordered dict, so its doc order is
// deterministic (repo_root then cwd, each in DOC_NAMES order). ProjectDocs is a Go map,
// whose iteration order is random — so the project_docs block here is not order-stable
// the way Python's is. Matching Python exactly would need an ordered ProjectDocs.
func (ctx *WorkspaceContext) String() string {
	commits := "- none"
	if len(ctx.RecentCommits) > 0 {
		parts := make([]string, len(ctx.RecentCommits))
		for i, line := range ctx.RecentCommits {
			parts[i] = "- " + line
		}
		commits = strings.Join(parts, "\n")
	}

	docs := "- none"
	if len(ctx.ProjectDocs) > 0 {
		parts := make([]string, 0, len(ctx.ProjectDocs))
		for path, snippet := range ctx.ProjectDocs {
			parts = append(parts, "- "+path+"\n"+snippet)
		}
		docs = strings.Join(parts, "\n")
	}

	return strings.Join([]string{
		"Workspace:",
		"- cwd: " + ctx.Cwd,
		"- repo_root: " + ctx.RepoRoot,
		"- branch: " + ctx.Branch,
		"- default_branch: " + ctx.DefaultBranch,
		"- status:",
		ctx.Status,
		"- recent_commits:",
		commits,
		"- project_docs:",
		docs,
	}, "\n")
}
