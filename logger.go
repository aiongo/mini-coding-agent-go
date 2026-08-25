package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/afero"
)

// NewLogger builds the application *slog.Logger, writing to logPath only — no console output.
//
// Ported from xhs-cms-go's infra/logger.go, with the config-driven bits stripped out: nothing
// is read from a config file — everything is hardcoded at construction, and the log level is
// fixed at debug. Originally built on zap; now std log/slog, so the zap dependency is gone.
// Unlike the reference, which can tee to a rotated file via lumberjack (driven by
// cfg.Infra.LogFile), this variant appends to a single file at logPath with no rotation — no
// lumberjack dependency, consistent with this project's minimal-deps goal. The parent
// directory is created through a local afero.NewOsFs() (same pattern as readProjectDocs in
// workspace_context.go) and the file is opened with O_CREATE|O_WRONLY|O_APPEND; if the file
// cannot be opened it falls back to stderr so logging is not lost entirely. fx is not used:
// the logger is constructed directly here and injected into the agent and model client (see
// NewMiniAgent), not provided through DI. The timestamp format "2006-01-02 15:04:05.000" is
// kept from the zap version via ReplaceAttr on the TextHandler.
func NewLogger(logPath string) *slog.Logger {
	fs := afero.NewOsFs()
	_ = fs.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := fs.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)

	var w io.Writer = os.Stderr // fall back to stderr only when the log file is unavailable
	if err == nil {
		w = f
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.String(slog.TimeKey, t.Format("2006-01-02 15:04:05.000"))
				}
			}
			return a
		},
	}))
}
