package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Now returns the current UTC time as an RFC3339 string (Python: now() -> UTC ISO8601),
// used for session and history timestamps.
func Now() string {
	return time.Now().Format(time.RFC3339)
}

// Clip truncates text to at most `limit` code points, appending a notice when it
// truncates. Mirrors Python clip() — counts/truncates by rune (not byte) so it
// never splits a multi-byte UTF-8 character and matches Python's len() semantics.
func Clip(text string, limit int) string {
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return fmt.Sprintf("%s\n...[truncated %d chars]", string(r[:limit]), len(r)-limit)
}

// Middle collapses newlines to spaces and shortens text to fit `limit` by keeping
// the head and tail with "..." between. Mirrors Python middle().
func Middle(text string, limit int) string {
	r := []rune(strings.ReplaceAll(text, "\n", " "))
	if len(r) <= limit {
		return string(r)
	}
	if limit <= 3 {
		return string(r[:limit])
	}
	left := (limit - 3) / 2
	right := limit - 3 - left
	return string(r[:left]) + "..." + string(r[len(r)-right:])
}

// ProcessResult holds the captured output of a finished process, mirroring the useful
// fields of Python's subprocess.CompletedProcess (returncode, stdout, stderr).
type ProcessResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// CmdNotFoundError is returned by RunProcess when the executable cannot be found — the Go
// equivalent of a failed shutil.which()/which (exec.LookPath found nothing in $PATH).
type CmdNotFoundError struct {
	Name string
}

func (e *CmdNotFoundError) Error() string {
	return fmt.Sprintf("command not found: %s", e.Name)
}

// IsCmdNotFoundError reports whether err is or wraps a CmdNotFoundError.
func IsCmdNotFoundError(err error) bool {
	_, ok := errors.AsType[*CmdNotFoundError](err)
	return ok
}

// RunProcess runs name with args in cwd, capturing stdout and stderr as text, and returns
// the result. It mirrors Python subprocess.run(capture_output=True, text=True, timeout=...):
//
//   - A non-zero exit is reported in ExitCode, NOT as an error (matching subprocess.run
//     without check=True). Callers wanting check=True semantics test ExitCode themselves.
//   - The returned error is for failures to start or wait on the process, including the
//     timeout/context deadline being exceeded (the Python equivalent is TimeoutExpired).
//   - A missing executable is reported as *CmdNotFoundError (test with IsCmdNotFoundError),
//     mirroring a failed shutil.which.
//   - timeout <= 0 means no timeout.
//
// For a shell command (Python shell=True), run it as
// RunProcess(ctx, "sh", []string{"-c", command}, cwd, timeout).
//
// This is the single helper behind Python's three subprocess.run call sites: the git
// helper (caller falls back on error/non-zero exit), ripgrep search, and the run_shell tool.
func RunProcess(ctx context.Context, name string, args []string, cwd string, timeoutSecs int) (*ProcessResult, error) {
	if timeoutSecs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &ProcessResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode(err),
	}

	// Context canceled/timed out (Python: TimeoutExpired) -> surface as an error.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}
	// Executable not found (exec.LookPath failed — the Go "which"); report it distinctly so
	// callers can fall back (e.g. search falls back to a stdlib grep when rg is missing).
	if _, ok := errors.AsType[*exec.Error](err); ok {
		return result, &CmdNotFoundError{Name: name}
	}
	// Non-zero exit (*exec.ExitError) is not an error here (subprocess.run without check=True).
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return result, nil
	}
	return result, err
}

// exitCode returns the process exit code from a cmd.Run() error, or -1 if the process did
// not exit normally (e.g. failed to start, or was killed by a signal / context deadline).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return -1
}
