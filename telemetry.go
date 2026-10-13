package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AgentCallRecord represents a single tool invocation performed by an agent.
type AgentCallRecord struct {
	Timestamp  string         `json:"timestamp"`
	Tool       string         `json:"tool"`
	DurationMs int64          `json:"duration_ms"`
	Success    bool           `json:"success"`
	Error      string         `json:"error,omitempty"`
	ErrorClass string         `json:"error_class,omitempty"`
	ActionID   string         `json:"action_id,omitempty"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	// Instance-level fields stamp which argus process / tool surface produced
	// the record, so abandonment can be attributed to a specific tool surface.
	InstanceID string `json:"instance_id,omitempty"`
	Surface    string `json:"surface,omitempty"`
	Pane       string `json:"pane,omitempty"`
}

// TelemetryLogger manages asynchronous, non-blocking disk logging of agent tool calls.
type TelemetryLogger struct {
	mu         sync.Mutex
	filePath   string
	ch         chan *AgentCallRecord
	done       chan struct{}
	closed     bool
	disabled   bool
	instanceID string
	surface    string
	pane       string
}

var globalTelemetry *TelemetryLogger

// defaultUsageLogPath returns the default path for agent telemetry records.
func defaultUsageLogPath() string {
	if p := os.Getenv("ARGUS_USAGE_LOG"); p != "" {
		return p
	}
	return filepath.Join(homeDir(), ".config", "argus", "agent_usage.jsonl")
}

// initTelemetry initializes the global telemetry logger.
func initTelemetry() *TelemetryLogger {
	if os.Getenv("ARGUS_DISABLE_TELEMETRY") == "1" || os.Getenv("ARGUS_DISABLE_TELEMETRY") == "true" {
		return &TelemetryLogger{disabled: true}
	}

	path := defaultUsageLogPath()
	tl := &TelemetryLogger{
		filePath:   path,
		ch:         make(chan *AgentCallRecord, 2048),
		done:       make(chan struct{}),
		instanceID: newInstanceID(),
		surface:    telemetrySurface(),
		pane:       os.Getenv("TMUX_PANE"),
	}

	go tl.worker()

	sd := NewShutdown()
	RegisterShutdown(sd)
	go func() {
		<-sd.begin
		tl.Close()
		sd.Complete()
	}()

	return tl
}

// Record queues an invocation record asynchronously. Never blocks caller goroutine.
func (tl *TelemetryLogger) Record(rec *AgentCallRecord) {
	if tl == nil || tl.disabled {
		return
	}
	tl.mu.Lock()
	if tl.closed {
		tl.mu.Unlock()
		return
	}
	tl.mu.Unlock()

	rec.InstanceID = tl.instanceID
	rec.Surface = tl.surface
	rec.Pane = tl.pane

	select {
	case tl.ch <- rec:
	default:
		// Buffer full: drop record rather than blocking agent interaction loop
		logWarn("telemetry buffer full: dropped record for %s", rec.Tool)
	}
}

// worker drains the record channel and flushes JSON lines to disk.
func (tl *TelemetryLogger) worker() {
	defer close(tl.done)

	var f *os.File
	var currentPath string

	ensureFile := func() (*os.File, error) {
		if f != nil && currentPath == tl.filePath {
			return f, nil
		}
		if f != nil {
			_ = f.Close()
			f = nil
		}
		dir := filepath.Dir(tl.filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(tl.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		f = file
		currentPath = tl.filePath
		return f, nil
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case rec, ok := <-tl.ch:
			if !ok {
				// Drained channel upon Close()
				if f != nil {
					_ = f.Sync()
					_ = f.Close()
				}
				return
			}
			file, err := ensureFile()
			if err != nil {
				logWarn("telemetry failed to open log file %s: %v", tl.filePath, err)
				continue
			}
			data, err := json.Marshal(rec)
			if err == nil {
				data = append(data, '\n')
				_, _ = file.Write(data)
			}
		case <-ticker.C:
			if f != nil {
				_ = f.Sync()
			}
		}
	}
}

// Close gracefully flushes remaining queue records and closes background logger.
func (tl *TelemetryLogger) Close() {
	if tl == nil || tl.disabled {
		return
	}
	tl.mu.Lock()
	if tl.closed {
		tl.mu.Unlock()
		return
	}
	tl.closed = true
	close(tl.ch)
	tl.mu.Unlock()

	select {
	case <-tl.done:
	case <-time.After(2 * time.Second):
		logWarn("telemetry flush timed out during shutdown")
	}
}

// recordToolCall is a convenience helper invoked by the tool execution wrapper.
func recordToolCall(toolName string, input any, duration time.Duration, err error, out any) {
	if globalTelemetry == nil || globalTelemetry.disabled {
		return
	}

	rec := &AgentCallRecord{
		Timestamp:  time.Now().Format(time.RFC3339Nano),
		Tool:       toolName,
		DurationMs: duration.Milliseconds(),
		Success:    err == nil,
	}

	if err != nil {
		rec.Error = err.Error()
		rec.ErrorClass = classifyError(rec.Error)
	}

	// Extract arguments map via JSON reflection
	if input != nil {
		if raw, mErr := json.Marshal(input); mErr == nil {
			var args map[string]any
			if uErr := json.Unmarshal(raw, &args); uErr == nil && len(args) > 0 {
				rec.Arguments = args
			}
		}
	}

	// Extract ActionID if present in output
	if out != nil {
		if m, ok := out.(map[string]any); ok {
			if actID, ok := m["action_id"].(string); ok && actID != "" {
				rec.ActionID = actID
			}
		}
	}

	globalTelemetry.Record(rec)
}

// newInstanceID returns a short random ID identifying one argus process run.
func newInstanceID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("inst-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// telemetrySurface describes the active tool surface so records can be
// attributed to a specific tool-set configuration after future consolidation.
func telemetrySurface() string {
	return fmt.Sprintf("%s/%d", argusVersion, len(enabledTools))
}

// classifyError buckets error text into the taxonomy established by the
// session-history mining (docs/argus-usability-mining-2026-10-13.md).
func classifyError(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "no target selected"):
		return "no_target"
	case strings.Contains(m, "no tab matching"):
		return "target_lost"
	case strings.Contains(m, "target not found"),
		strings.Contains(m, "tab not found"),
		strings.Contains(m, "target closed"),
		strings.Contains(m, "promise was collected"):
		return "target_lost"
	case strings.Contains(m, "browser evaluation failed"),
		strings.Contains(m, "syntaxerror"),
		strings.Contains(m, "typeerror"),
		strings.Contains(m, "referenceerror"):
		return "eval_js_error"
	case strings.Contains(m, "not clickable"),
		strings.Contains(m, "not visible"),
		strings.Contains(m, "not editable"),
		strings.Contains(m, "not ready"),
		strings.Contains(m, "not interactable"):
		return "element_not_ready"
	case strings.Contains(m, "element not found"),
		strings.Contains(m, "no element matching"),
		strings.Contains(m, "cannot find element"),
		strings.Contains(m, "no element found"):
		return "element_not_found"
	case strings.Contains(m, "timeout"), strings.Contains(m, "timed out"),
		strings.Contains(m, "deadline exceeded"):
		return "timeout"
	case strings.Contains(m, "dialog") && strings.Contains(m, "open"):
		return "dialog_blocked"
	case strings.Contains(m, "navigation"),
		strings.Contains(m, "net::"):
		return "navigation"
	case strings.Contains(m, "invalid"),
		strings.Contains(m, "requires"),
		strings.Contains(m, "unknown device"),
		strings.Contains(m, "unknown tool"),
		strings.Contains(m, "missing"):
		return "arg_validation"
	default:
		return "other"
	}
}
