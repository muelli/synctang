// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"sync"
)

// The bridge's own log, for someone following what the app does from adb logcat.
//
// What gomobile does with output, read in golang.org/x/mobile/internal/mobileinit:
// on Android it redirects stderr and stdout into pipes and writes each line to logcat
// under the tag GoLog, stderr at ERROR priority and stdout at INFO, and sets the
// standard library's log package to write to logcat at INFO. So writing a trace to
// stderr would make every line an error in logcat. This writes through log.Writer(),
// which on Android is gomobile's INFO writer and anywhere else is stderr, and
// resolves it at each write because mobileinit sets it up during start-up, after
// package initialisation.
//
// The default level is warn, so a release build says almost nothing; the app turns
// it up with SetLogLevel in a debug build.

// levelTrace is below slog's lowest built-in level, the same value
// mrcore/transport uses for connection lifecycle detail, so that the transports
// given this logger write at the level they already mean.
const levelTrace = slog.Level(-8)

var (
	logLevel slog.LevelVar // zero value is info; set to warn in init
	logMu    sync.Mutex
	logOut   io.Writer // nil means log.Writer()
	logger   *slog.Logger
)

func init() {
	logLevel.Set(slog.LevelWarn)
	logger = slog.New(slog.NewTextHandler(logWriter{}, &slog.HandlerOptions{
		Level: &logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				if l, ok := a.Value.Any().(slog.Level); ok && l == levelTrace {
					return slog.String(slog.LevelKey, "TRACE")
				}
			}
			return a
		},
	}))
}

// logWriter writes to where the log goes now.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	logMu.Lock()
	out := logOut
	logMu.Unlock()
	if out == nil {
		out = log.Writer()
	}
	return out.Write(p)
}

// setLogOutput redirects the bridge's log, for the tests; nil puts it back.
func setLogOutput(w io.Writer) {
	logMu.Lock()
	defer logMu.Unlock()
	logOut = w
}

// bridgeLog is the logger every part of the bridge uses, handed to the transports
// as well, so one level controls all of it.
func bridgeLog() *slog.Logger { return logger }

// SetLogLevel sets how much the bridge logs: trace, debug, info, warn or error. A
// level that is none of those is an error and changes nothing; there is no quiet
// fallback, so a typo in the app cannot silently turn logging off or on.
func SetLogLevel(level string) error {
	var l slog.Level
	switch level {
	case "trace":
		l = levelTrace
	case "debug":
		l = slog.LevelDebug
	case "info":
		l = slog.LevelInfo
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		return fmt.Errorf("mobile: unknown log level %q, want one of trace, debug, info, warn, error", level)
	}
	logLevel.Set(l)
	return nil
}

// stageName is a stage in words, for the log.
func stageName(stage int) string {
	switch stage {
	case StageNone:
		return "none"
	case StageStarting:
		return "starting"
	case StageLookingUp:
		return "looking_up"
	case StageConnecting:
		return "connecting"
	case StageCheckingIdentity:
		return "checking_identity"
	case StageWaitingForRequest:
		return "waiting_for_request"
	case StageRequestReady:
		return "request_ready"
	case StageSendingAnswer:
		return "sending_answer"
	case StageWaitingForVerdict:
		return "waiting_for_verdict"
	case StageUnlocked:
		return "unlocked"
	case StageSendingProof:
		return "sending_proof"
	case StageWaitingForOperator:
		return "waiting_for_operator"
	case StageEnrolled:
		return "enrolled"
	}
	return fmt.Sprintf("stage_%d", stage)
}

func routeName(route int) string {
	switch route {
	case RouteRelay:
		return "relay"
	case RouteLocal:
		return "local"
	case RouteDirect:
		return "direct"
	}
	return "unknown"
}

// kidPrefix is the first eight hex characters of a key identifier: enough to tell
// sessions apart, and not the identifier.
func kidPrefix(kid []byte) string {
	s := fmt.Sprintf("%x", kid)
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

func logEvent(level slog.Level, msg string, args ...any) {
	logger.Log(context.Background(), level, msg, args...)
}
