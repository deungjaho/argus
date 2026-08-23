package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// --- Structured logging ---
//
// Minimal structured logger with levels and prefix support.
// Inspired by ngrok's log.Logger interface but self-contained (no external deps).
// Every connection and goroutine can add prefixes for traceability.

type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
	LogError
)

var (
	logMu     sync.Mutex
	logLevel  = LogInfo
	logPrefix = "argus"
)

func init() {
	switch os.Getenv("ARGUS_LOG") {
	case "debug":
		logLevel = LogDebug
	case "warn":
		logLevel = LogWarn
	case "error":
		logLevel = LogError
	default:
		logLevel = LogInfo
	}
}

func levelName(l LogLevel) string {
	switch l {
	case LogDebug:
		return "DEBUG"
	case LogInfo:
		return "INFO"
	case LogWarn:
		return "WARN"
	case LogError:
		return "ERROR"
	default:
		return "?"
	}
}

// Logger is a prefixed structured logger.
type Logger struct {
	prefix string
}

// NewLogger creates a logger with the given prefix.
func NewLogger(prefix string) *Logger {
	return &Logger{prefix: prefix}
}

// WithPrefix returns a new logger with an additional prefix segment.
func (l *Logger) WithPrefix(extra string) *Logger {
	if l.prefix == "" {
		return &Logger{prefix: extra}
	}
	return &Logger{prefix: l.prefix + " " + extra}
}

func (l *Logger) log(level LogLevel, format string, args ...any) {
	if level < logLevel {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	if l.prefix != "" {
		fmt.Fprintf(os.Stderr, "%s [%s] [%s] %s\n", ts, levelName(level), l.prefix, msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s [%s] %s\n", ts, levelName(level), msg)
	}
}

func (l *Logger) Debug(format string, args ...any) { l.log(LogDebug, format, args...) }
func (l *Logger) Info(format string, args ...any)  { l.log(LogInfo, format, args...) }
func (l *Logger) Warn(format string, args ...any)  { l.log(LogWarn, format, args...) }
func (l *Logger) Error(format string, args ...any) { l.log(LogError, format, args...) }

// Package-level logger for contexts without a specific Logger instance.
var defaultLogger = NewLogger("argus")

func logDebug(format string, args ...any) { defaultLogger.log(LogDebug, format, args...) }
func logInfo(format string, args ...any)  { defaultLogger.log(LogInfo, format, args...) }
func logWarn(format string, args ...any)  { defaultLogger.log(LogWarn, format, args...) }
func logError(format string, args ...any) { defaultLogger.log(LogError, format, args...) }
