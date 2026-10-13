package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelemetryLogger_RecordAndFlush(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test_usage.jsonl")

	tl := &TelemetryLogger{
		filePath: logPath,
		ch:       make(chan *AgentCallRecord, 100),
		done:     make(chan struct{}),
	}
	go tl.worker()

	// Record sample calls
	tl.Record(&AgentCallRecord{
		Timestamp:  time.Now().Format(time.RFC3339Nano),
		Tool:       "eval",
		DurationMs: 12,
		Success:    true,
		Arguments:  map[string]any{"js": "document.title", "target": "tab_1"},
		ActionID:   "act_001",
	})

	tl.Record(&AgentCallRecord{
		Timestamp:  time.Now().Format(time.RFC3339Nano),
		Tool:       "click",
		DurationMs: 45,
		Success:    false,
		Error:      "element not found: #submit",
		Arguments:  map[string]any{"selector": "#submit"},
	})

	// Graceful close flushes records
	tl.Close()

	// Verify file was written
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read written telemetry log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines of telemetry, got %d:\n%s", len(lines), string(data))
	}

	var rec1 AgentCallRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec1); err != nil {
		t.Fatalf("line 1 json invalid: %v", err)
	}
	if rec1.Tool != "eval" || !rec1.Success || rec1.Arguments["js"] != "document.title" {
		t.Errorf("unexpected record 1 content: %+v", rec1)
	}

	var rec2 AgentCallRecord
	if err := json.Unmarshal([]byte(lines[1]), &rec2); err != nil {
		t.Fatalf("line 2 json invalid: %v", err)
	}
	if rec2.Tool != "click" || rec2.Success || rec2.Error != "element not found: #submit" {
		t.Errorf("unexpected record 2 content: %+v", rec2)
	}
}

func TestAnalyzeUsageLog(t *testing.T) {
	buf := new(bytes.Buffer)
	now := time.Now()

	records := []AgentCallRecord{
		{
			Timestamp:  now.Add(-2 * time.Minute).Format(time.RFC3339Nano),
			Tool:       "eval",
			DurationMs: 10,
			Success:    true,
			Arguments:  map[string]any{"js": "1+1"},
		},
		{
			Timestamp:  now.Add(-1 * time.Minute).Format(time.RFC3339Nano),
			Tool:       "eval",
			DurationMs: 20,
			Success:    true,
			Arguments:  map[string]any{"js": "2+2", "target": "http://localhost:5174"},
		},
		{
			Timestamp:  now.Format(time.RFC3339Nano),
			Tool:       "click",
			DurationMs: 100,
			Success:    false,
			Error:      "timeout waiting for element",
			Arguments:  map[string]any{"selector": "button.submit"},
		},
	}

	for _, r := range records {
		data, _ := json.Marshal(r)
		buf.Write(data)
		buf.WriteByte('\n')
	}

	summary, err := AnalyzeUsageLog(buf)
	if err != nil {
		t.Fatalf("AnalyzeUsageLog failed: %v", err)
	}

	if summary.TotalCalls != 3 {
		t.Errorf("expected 3 total calls, got %d", summary.TotalCalls)
	}
	if summary.TotalSuccesses != 2 || summary.TotalErrors != 1 {
		t.Errorf("expected 2 ok, 1 err; got %d ok, %d err", summary.TotalSuccesses, summary.TotalErrors)
	}

	evalStat := summary.Tools["eval"]
	if evalStat == nil || evalStat.Calls != 2 {
		t.Fatalf("expected eval calls 2, got %+v", evalStat)
	}
	if evalStat.ArgKeys["js"] != 2 || evalStat.ArgKeys["target"] != 1 {
		t.Errorf("unexpected eval arg habits: %+v", evalStat.ArgKeys)
	}

	clickStat := summary.Tools["click"]
	if clickStat == nil || clickStat.Errors != 1 {
		t.Fatalf("expected click errors 1, got %+v", clickStat)
	}
}

func TestPrintUsageReport(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "sample_usage.jsonl")

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		rec := AgentCallRecord{
			Timestamp:  time.Now().Format(time.RFC3339Nano),
			Tool:       "status",
			DurationMs: int64(5 + i),
			Success:    true,
			Arguments:  map[string]any{"verbose": i%2 == 0},
		}
		data, _ := json.Marshal(rec)
		fmt.Fprintf(f, "%s\n", data)
	}
	f.Close()

	if err := PrintUsageReport(logPath); err != nil {
		t.Fatalf("PrintUsageReport failed: %v", err)
	}
}
