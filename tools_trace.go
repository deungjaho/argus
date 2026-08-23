package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// timelineEntry is a unified entry in the time-ordered timeline stream.
// Both events and actions are represented as timeline entries.
type timelineEntry struct {
	Seq         int    `json:"seq"`
	Kind        string `json:"kind"` // "event" or "action"
	Timestamp   string `json:"timestamp"`
	TargetID    string `json:"target_id"`
	EventID     string `json:"event_id,omitempty"`
	EventType   string `json:"type,omitempty"`
	ActionID    string `json:"action_id,omitempty"`
	Attribution string `json:"attribution,omitempty"` // "explicit", "time_overlap", or "exact_cdp_id"
	Confidence  string `json:"confidence,omitempty"`  // "high" or "heuristic"
	// LinkedIDs holds the CDP identifiers this event carries (frame_id,
	// loader_id, request_id, parent_frame_id). Populated for navigation
	// and network events so consumers can join entries by CDP ID.
	LinkedIDs map[string]string `json:"linked_ids,omitempty"`
	// LinkedBy identifies the CDP ID that deterministically links this
	// event to at least one other event in the same timeline result,
	// e.g. "loader_id:ABC123". Only set when Attribution is
	// "exact_cdp_id". The link is scoped to the entry's TargetID — CDP
	// IDs are per-target, so the same value in different tabs is not a
	// link.
	LinkedBy   string `json:"linked_by,omitempty"`
	Data       any    `json:"data,omitempty"`
	Op         string `json:"op,omitempty"`
	StartTime  string `json:"start_time,omitempty"`
	EndTime    string `json:"end_time,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

func registerTraceTools(server *mcp.Server) {
	type timelineInput struct {
		Limit          int    `json:"limit,omitempty"`
		EventType      string `json:"event_type,omitempty"`
		ActionID       string `json:"action_id,omitempty"`
		TargetID       string `json:"target_id,omitempty"`
		IncludeActions bool   `json:"include_actions,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name: "timeline",
		Description: "Query the flight recorder for a single time-ordered stream of browser events " +
			"and agent actions. Results are merged by timestamp (RFC3339Nano) with a sequence number. " +
			"Each entry has a 'kind' field ('event' or 'action'), and events include attribution " +
			"method and confidence. Attribution is 'explicit' for actions, 'time_overlap' (heuristic) " +
			"for events attributed to an action by time, or 'exact_cdp_id' (high confidence) for " +
			"events deterministically linked to other events by a shared CDP identifier (loaderId, " +
			"frameId, requestId). Navigation and network events expose their CDP IDs in 'linked_ids' " +
			"and, when linked, the joining key in 'linked_by'. " +
			"Filter by event_type, action_id, or target_id. " +
			"Default limit: 50 entries. Set include_actions=true to include actions " +
			"(default: events only).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input timelineInput) (*mcp.CallToolResult, any, error) {
		totalLimit := input.Limit
		if totalLimit <= 0 {
			totalLimit = 50
		}

		eventType := strings.TrimSpace(input.EventType)
		actionID := strings.TrimSpace(input.ActionID)
		targetID := strings.TrimSpace(input.TargetID)

		// Query events
		evtFilter := EventFilter{
			eventType: eventType,
			actionID:  actionID,
			targetID:  targetID,
			limit:     totalLimit,
		}
		events := rec.QueryEvents(evtFilter)

		// Optionally query actions
		var actions []ActionRecord
		if input.IncludeActions {
			actFilter := ActionFilter{
				targetID: targetID,
				limit:    totalLimit,
			}
			actions = rec.QueryActions(actFilter)
		}

		// Build the unified timeline (merge, sort, limit, CDP-ID link).
		entries := buildTimeline(events, actions, totalLimit)

		result := map[string]any{
			"timeline":    entries,
			"entry_count": len(entries),
			"target_id":   targetID,
		}

		// Include recorder stats
		stats := rec.Stats()
		result["recorder"] = map[string]any{
			"actions_recorded":    stats.ActionsRecorded,
			"events_recorded":     stats.EventsRecorded,
			"action_capacity":     stats.ActionCapacity,
			"event_capacity":      stats.EventCapacity,
			"events_dropped":      stats.EventsDropped,
			"data_truncated":      stats.DataTruncated,
			"domain_enable_error": stats.DomainEnableError,
			"reconnect_gaps":      stats.ReconnectGaps,
		}

		return nil, result, nil
	})

	type diagnoseInput struct {
		Limit    int    `json:"limit,omitempty"`
		TargetID string `json:"target_id,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name: "diagnose",
		Description: "Get a diagnostic snapshot of the current page state: recent console errors/warnings, " +
			"uncaught exceptions, recent failed actions, and current page status. " +
			"Combines data from the flight recorder with a live status check. " +
			"Use this when something goes wrong and you need a quick overview of what happened. " +
			"Filter by target_id to scope to a specific tab; defaults to current target. " +
			"Default limit: 10 events per category.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input diagnoseInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 10
		}

		// Scope to target_id if provided, otherwise use current target
		targetID := strings.TrimSpace(input.TargetID)
		if targetID == "" {
			targetID = getCurrentTargetID()
		}

		result := map[string]any{
			"target_id": targetID,
		}

		// 1. Current page status (redacted)
		var status map[string]any
		if activeBackend() == BackendWeChat {
			status, _ = wechatStatus()
		} else {
			status, _ = browserStatus()
		}
		if status != nil {
			result["status"] = redactData(status)
		}

		if activeBackend() == BackendWeChat {
			// WeChat backend: use automator event ring buffer for console/error evidence.
			// Enable log subscription (idempotent) to ensure events are captured.
			_ = automatorEnableLog()

			// Fetch bounded recent logs (warnings/errors only by default).
			maxChars := limit * 200 // bounded: ~200 chars per entry
			logs, _, total := automatorRecentLogs(false, limit, maxChars)
			consoleIssues := make([]map[string]any, 0, len(logs))
			for _, l := range logs {
				level, _ := l["level"].(string)
				if level != "warn" && level != "error" {
					continue
				}
				entry := map[string]any{
					"timestamp": l["timestamp"],
					"level":     level,
					"text":      redactString(fmt.Sprintf("%v", l["text"])),
				}
				if seq, ok := l["seq"]; ok {
					entry["seq"] = seq
				}
				consoleIssues = append(consoleIssues, entry)
			}
			result["console_issues"] = consoleIssues
			result["console_issue_count"] = len(consoleIssues)

			// WeChat exceptions are classified from log events.
			allLogs, _, _ := automatorRecentLogs(true, limit, maxChars)
			exceptions := make([]map[string]any, 0)
			for _, l := range allLogs {
				level, _ := l["level"].(string)
				if level == "error" {
					exceptions = append(exceptions, map[string]any{
						"timestamp": l["timestamp"],
						"text":      redactString(fmt.Sprintf("%v", l["text"])),
						"seq":       l["seq"],
					})
				}
			}
			result["exceptions"] = exceptions
			result["exception_count"] = len(exceptions)

			// Recent failed actions from the action recorder.
			actions := rec.QueryActions(ActionFilter{targetID: targetID, limit: limit * 2})
			failedActions := []map[string]any{}
			for _, a := range actions {
				if a.Error == nil {
					continue
				}
				failedActions = append(failedActions, map[string]any{
					"action_id":  string(a.ID),
					"op":         a.Op,
					"target_id":  a.TargetID,
					"error":      a.Error.Message,
					"start_time": a.StartTime.Format(time.RFC3339Nano),
				})
			}
			result["failed_actions"] = failedActions
			result["failed_action_count"] = len(failedActions)

			// Event ring buffer stats.
			conn, _ := getAutomatorConn()
			if conn != nil {
				droppedCount, truncatedCount, bytesIn := conn.eventStats()
				result["recorder"] = map[string]any{
					"events_dropped":   droppedCount,
					"events_truncated": truncatedCount,
					"event_bytes_in":   bytesIn,
					"total_events":     total,
				}
			}

			// Summary.
			issueCount := len(consoleIssues) + len(exceptions) + len(failedActions)
			if issueCount == 0 {
				result["summary"] = "no issues found in recent history"
			} else {
				result["summary"] = formatDiagnoseSummary(len(consoleIssues), len(exceptions), len(failedActions))
			}

			return nil, result, nil
		}

		// CDP backend: use the flight recorder.
		// 2. Recent console errors and warnings (target-scoped)
		consoleEvents := rec.QueryEvents(EventFilter{
			eventType: "console",
			targetID:  targetID,
			limit:     limit,
		})
		consoleIssues := []map[string]any{}
		for _, e := range consoleEvents {
			if e.Data == nil {
				continue
			}
			data, ok := e.Data.(map[string]any)
			if !ok {
				continue
			}
			level, _ := data["level"].(string)
			if level == "error" || level == "warning" {
				textStr, _ := data["text"].(string)
				consoleIssues = append(consoleIssues, map[string]any{
					"timestamp": e.Timestamp.Format(time.RFC3339Nano),
					"level":     level,
					"text":      redactString(textStr),
					"action_id": string(e.ActionID),
				})
			}
		}
		result["console_issues"] = consoleIssues
		result["console_issue_count"] = len(consoleIssues)

		// 3. Uncaught exceptions (target-scoped)
		exceptionEvents := rec.QueryEvents(EventFilter{
			eventType: "exception",
			targetID:  targetID,
			limit:     limit,
		})
		exceptions := []map[string]any{}
		for _, e := range exceptionEvents {
			text := ""
			if e.Data != nil {
				if data, ok := e.Data.(map[string]any); ok {
					text, _ = data["text"].(string)
				}
			}
			exceptions = append(exceptions, map[string]any{
				"timestamp": e.Timestamp.Format(time.RFC3339Nano),
				"text":      redactString(text),
				"action_id": string(e.ActionID),
			})
		}
		result["exceptions"] = exceptions
		result["exception_count"] = len(exceptions)

		// 4. Recent failed actions (target-scoped)
		actions := rec.QueryActions(ActionFilter{targetID: targetID, limit: limit * 2})
		failedActions := []map[string]any{}
		for _, a := range actions {
			if a.Error == nil {
				continue
			}
			failedActions = append(failedActions, map[string]any{
				"action_id":  string(a.ID),
				"op":         a.Op,
				"target_id":  a.TargetID,
				"error":      a.Error.Message,
				"start_time": a.StartTime.Format(time.RFC3339Nano),
			})
		}
		result["failed_actions"] = failedActions
		result["failed_action_count"] = len(failedActions)

		// 5. Recorder stats
		stats := rec.Stats()
		result["recorder"] = map[string]any{
			"actions_recorded":    stats.ActionsRecorded,
			"events_recorded":     stats.EventsRecorded,
			"events_dropped":      stats.EventsDropped,
			"data_truncated":      stats.DataTruncated,
			"domain_enable_error": stats.DomainEnableError,
			"reconnect_gaps":      stats.ReconnectGaps,
		}

		// 6. Summary
		issueCount := len(consoleIssues) + len(exceptions) + len(failedActions)
		if issueCount == 0 {
			result["summary"] = "no issues found in recent history"
		} else {
			result["summary"] = formatDiagnoseSummary(len(consoleIssues), len(exceptions), len(failedActions))
		}

		return nil, result, nil
	})

	type exportTraceInput struct {
		Path     string `json:"path,omitempty"`
		ActionID string `json:"action_id,omitempty"`
		TargetID string `json:"target_id,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name: "export_trace",
		Description: "Export a trace bundle (JSON) containing recent actions, events, and page status " +
			"from the flight recorder. Useful for saving evidence of a debugging session or " +
			"sharing with another agent. Files are written to a controlled artifact directory " +
			"(~/.local/share/argus/traces/ or ARGUS_TRACE_DIR) with mode 0600. " +
			"Existing files are not overwritten (use a different filename). " +
			"Filter by action_id or target_id to export a specific subset.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input exportTraceInput) (*mcp.CallToolResult, any, error) {
		actionID := strings.TrimSpace(input.ActionID)
		targetID := strings.TrimSpace(input.TargetID)

		// Gather all data
		trace := map[string]any{
			"exported_at": time.Now().Format(time.RFC3339),
			"version":     argusVersion,
		}

		// Actions — redact params/result for full security
		actFilter := ActionFilter{targetID: targetID, limit: 200}
		actions := rec.QueryActions(actFilter)
		if actionID != "" {
			filtered := []ActionRecord{}
			for _, a := range actions {
				if string(a.ID) == actionID {
					filtered = append(filtered, a)
				}
			}
			actions = filtered
		}
		// Redact sensitive data in action params and results
		redactedActions := make([]ActionRecord, len(actions))
		for i, a := range actions {
			a.Params = redactData(a.Params)
			a.Result = redactData(a.Result)
			redactedActions[i] = a
		}
		trace["actions"] = redactedActions

		// Events — redact sensitive data
		evtFilter := EventFilter{
			actionID: actionID,
			targetID: targetID,
			limit:    2000,
		}
		events := rec.QueryEvents(evtFilter)
		redactedEvents := make([]EventRecord, len(events))
		for i, e := range events {
			e.Data = redactData(e.Data)
			redactedEvents[i] = e
		}
		trace["events"] = redactedEvents

		// Status — redact sensitive data
		status, _ := browserStatus()
		if status != nil {
			trace["status"] = redactData(status)
		}

		// Recorder stats
		trace["recorder"] = rec.Stats()

		// Resolve output path in controlled artifact directory
		traceDir := getTraceDir()
		if err := os.MkdirAll(traceDir, 0700); err != nil {
			return nil, nil, fmt.Errorf("failed to create trace directory %s: %w", traceDir, err)
		}

		filename := strings.TrimSpace(input.Path)
		if filename == "" {
			filename = fmt.Sprintf("argus-trace-%d.json", time.Now().UnixNano())
		}
		// If the path contains a separator, take only the base name
		// to prevent path traversal outside the trace directory.
		filename = filepath.Base(filename)
		if filename == "" || filename == "." || filename == ".." {
			filename = fmt.Sprintf("argus-trace-%d.json", time.Now().UnixNano())
		}
		path := filepath.Join(traceDir, filename)

		// Refuse to overwrite existing files
		if _, err := os.Stat(path); err == nil {
			return nil, nil, fmt.Errorf("trace file already exists: %s (use a different filename)", path)
		}

		data, err := json.MarshalIndent(trace, "", "  ")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal trace: %w", err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return nil, nil, fmt.Errorf("failed to write trace file: %w", err)
		}

		return nil, map[string]any{
			"path":         path,
			"bytes":        len(data),
			"action_count": len(actions),
			"event_count":  len(events),
		}, nil
	})
}

// buildTimeline merges event and action records into a single
// time-ordered timeline with CDP-ID link detection. It is a pure
// function over the provided records — all recorder queries happen
// before this call, making it straightforward to unit-test.
//
// Steps:
//  1. Convert each EventRecord and ActionRecord to a timelineEntry.
//     Events attributed to an action by time overlap keep
//     Attribution="time_overlap", Confidence="heuristic".
//  2. Sort all entries by timestamp (oldest first).
//  3. Apply the total limit (keep the most recent entries).
//  4. Detect exact CDP-ID links: events sharing a CDP identifier
//     (loaderId, frameId, requestId) within the same target are
//     labelled Attribution="exact_cdp_id", Confidence="high". This
//     deterministic link takes precedence over time-overlap
//     attribution. The ActionID field is still populated so consumers
//     can see both relationships.
//  5. Assign stable sequence numbers.
func buildTimeline(events []EventRecord, actions []ActionRecord, totalLimit int) []timelineEntry {
	entries := make([]timelineEntry, 0, len(events)+len(actions))

	for _, e := range events {
		entry := timelineEntry{
			Kind:      "event",
			Timestamp: e.Timestamp.Format(time.RFC3339Nano),
			TargetID:  e.TargetID,
			EventID:   string(e.ID),
			EventType: e.Type,
			Data:      redactData(e.Data),
		}
		if e.ActionID != "" {
			entry.ActionID = string(e.ActionID)
			entry.Attribution = "time_overlap"
			entry.Confidence = "heuristic"
		}
		linkedIDs := extractCDPIDs(e.Data)
		if len(linkedIDs) > 0 {
			entry.LinkedIDs = linkedIDs
		}
		entries = append(entries, entry)
	}

	for _, a := range actions {
		entry := timelineEntry{
			Kind:        "action",
			Timestamp:   a.StartTime.Format(time.RFC3339Nano),
			TargetID:    a.TargetID,
			Op:          a.Op,
			ActionID:    string(a.ID),
			StartTime:   a.StartTime.Format(time.RFC3339Nano),
			Attribution: "explicit",
			Confidence:  "high",
		}
		if a.EndTime != nil {
			entry.EndTime = a.EndTime.Format(time.RFC3339Nano)
			entry.DurationMs = a.EndTime.Sub(a.StartTime).Milliseconds()
		}
		if a.Error != nil {
			entry.Error = a.Error.Message
		}
		entries = append(entries, entry)
	}

	// Sort by timestamp (oldest first for timeline order)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp < entries[j].Timestamp
	})

	// Apply total limit after merge (keep most recent)
	if len(entries) > totalLimit {
		entries = entries[len(entries)-totalLimit:]
	}

	// Detect exact CDP-ID links on the visible result set.
	linkExactCDPIDs(entries)

	// Assign sequence numbers
	for i := range entries {
		entries[i].Seq = i + 1
	}

	return entries
}

// extractCDPIDs extracts the CDP identifiers (frameId, loaderId,
// requestId, parentFrameId) from an event's Data field. Returns an
// empty map for event types that do not carry CDP IDs (console,
// exception, log, reconnect).
//
// Handles both typed evidence structs (NavigationEvidence,
// NetworkEvidence) and map[string]any values produced by JSON
// round-trip or truncation.
func extractCDPIDs(data any) map[string]string {
	ids := map[string]string{}
	switch v := data.(type) {
	case NavigationEvidence:
		if v.FrameID != "" {
			ids["frame_id"] = v.FrameID
		}
		if v.LoaderID != "" {
			ids["loader_id"] = v.LoaderID
		}
		if v.ParentFrameID != "" {
			ids["parent_frame_id"] = v.ParentFrameID
		}
	case NetworkEvidence:
		if v.RequestID != "" {
			ids["request_id"] = v.RequestID
		}
		if v.LoaderID != "" {
			ids["loader_id"] = v.LoaderID
		}
		if v.FrameID != "" {
			ids["frame_id"] = v.FrameID
		}
	case map[string]any:
		for _, key := range []string{"frame_id", "loader_id", "parent_frame_id", "request_id"} {
			if s, ok := v[key].(string); ok && s != "" {
				ids[key] = s
			}
		}
	}
	return ids
}

// linkExactCDPIDs detects events that share a CDP identifier (frameId,
// loaderId, requestId) within the same target and labels them with
// Attribution="exact_cdp_id", Confidence="high". CDP IDs are scoped to
// a target's session, so the same ID value in different targets is not
// a link.
//
// This is a deterministic (non-heuristic) relationship: two events
// carrying the same loaderId are provably from the same loader. It is
// still an evidence relationship, not guaranteed causality — the ID
// match proves shared frame/loader identity, not that one event caused
// the other.
//
// Complexity is O(n*k) where n is the number of entries and k is the
// number of CDP IDs per entry (typically 1-3). The entry count is
// bounded by the timeline limit (default 50).
func linkExactCDPIDs(entries []timelineEntry) {
	// Count how many event entries carry each CDP ID, scoped by target.
	counts := map[string]int{}
	for _, e := range entries {
		if e.Kind != "event" {
			continue
		}
		for k, v := range e.LinkedIDs {
			if v == "" {
				continue
			}
			counts[e.TargetID+"\x00"+k+":"+v]++
		}
	}

	// Label event entries whose CDP IDs are shared by 2+ entries.
	// The first shared ID (map iteration order) wins for LinkedBy.
	for i := range entries {
		if entries[i].Kind != "event" || len(entries[i].LinkedIDs) == 0 {
			continue
		}
		for k, v := range entries[i].LinkedIDs {
			if v == "" {
				continue
			}
			if counts[entries[i].TargetID+"\x00"+k+":"+v] >= 2 {
				entries[i].Attribution = "exact_cdp_id"
				entries[i].Confidence = "high"
				entries[i].LinkedBy = k + ":" + v
				break
			}
		}
	}
}

func formatDiagnoseSummary(consoleIssues, exceptions, failedActions int) string {
	parts := []string{}
	if consoleIssues > 0 {
		parts = append(parts, formatDiagnoseCount(consoleIssues, "console issue"))
	}
	if exceptions > 0 {
		parts = append(parts, formatDiagnoseCount(exceptions, "exception"))
	}
	if failedActions > 0 {
		parts = append(parts, formatDiagnoseCount(failedActions, "failed action"))
	}
	return "found " + joinStrings(parts, ", ")
}

func formatDiagnoseCount(n int, label string) string {
	if n == 1 {
		return "1 " + label
	}
	return formatInt(n) + " " + label + "s"
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}

func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// getTraceDir returns the controlled directory for trace exports.
// Priority: ARGUS_TRACE_DIR env > ~/.local/share/argus/traces/
func getTraceDir() string {
	if dir := os.Getenv("ARGUS_TRACE_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "argus-traces")
	}
	return filepath.Join(home, ".local", "share", "argus", "traces")
}
