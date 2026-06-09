// Package loglevel exposes the process-wide atomic log level so the admin
// /loglevel endpoint can raise or lower verbosity at runtime (incident triage)
// without restarting the gateway. main() builds the zap logger with this level;
// the admin handler reads/sets it via zap's AtomicLevel HTTP handler.
package loglevel

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Level is the process-wide log level, default Info. Behaviour is unchanged
// from a fixed zap.NewProduction() logger unless an operator changes it.
var Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
