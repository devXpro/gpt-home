package relay

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	frplog "github.com/fatedier/frp/pkg/util/log"
	golog "github.com/fatedier/golib/log"
)

// frp logs through one process-wide logger (frp/pkg/util/log.Logger). It is replaced once, in init, before any frp
// goroutine can run, so the swap cannot race. Lines are then routed to the sink of the running Client.

// logSink receives frp log lines.
type logSink struct {
	log *slog.Logger
	// onClientError gets warn and error lines logged by frp's client package (login and proxy failures).
	onClientError func(msg string)
}

var activeSink atomic.Pointer[logSink]

func init() {
	frplog.Logger = frplog.Logger.WithOptions(
		golog.WithOutput(frpLogWriter{}),
		golog.WithLevel(golog.InfoLevel),
		golog.WithCaller(true), // the caller path tells client lines from others (frps in tests)
	)
}

// frpLogWriter implements golib's log.Writer, so it gets the level with every line.
type frpLogWriter struct{}

func (frpLogWriter) Write(p []byte) (int, error) { return len(p), nil }

func (frpLogWriter) WriteLog(p []byte, level golog.Level, _ time.Time) (int, error) {
	s := activeSink.Load()
	if s == nil {
		return len(p), nil
	}
	caller, msg := parseFrpLine(string(p))
	slvl := slog.LevelDebug
	switch level {
	case golog.InfoLevel:
		slvl = slog.LevelInfo
	case golog.WarnLevel:
		slvl = slog.LevelWarn
	case golog.ErrorLevel:
		slvl = slog.LevelError
	}
	if s.log != nil {
		s.log.Log(context.Background(), slvl, msg, "component", "frp", "src", caller)
	}
	if level >= golog.WarnLevel && strings.HasPrefix(caller, "client/") && s.onClientError != nil {
		s.onClientError(msg)
	}
	return len(p), nil
}

// parseFrpLine splits a golib line "2006-01-02 15:04:05.000 [W] [client/service.go:358] text\n" into the caller
// ("client/service.go:358") and the text.
func parseFrpLine(line string) (caller, msg string) {
	line = strings.TrimRight(line, "\n")
	// Skip the timestamp and the level tag: everything up to and including the first "] ".
	if _, rest, ok := strings.Cut(line, "] "); ok {
		line = rest
	}
	if strings.HasPrefix(line, "[") {
		if c, rest, ok := strings.Cut(line[1:], "] "); ok {
			return c, rest
		}
	}
	return "", line
}
