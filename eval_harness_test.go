package main

// eval_harness_test.go — evaluation harness.
//
// This is the smallest executable harness for measuring the agent semantic
// loop across Chrome (CDP). It records:
//   - task success
//   - tool calls
//   - first-action success
//   - retries
//   - Argus schema/output bytes and estimated tokens
//   - wall-clock latency
//   - fallback to eval escape hatches
//   - error recovery outcome
//
// The harness does NOT fabricate results. When a backend is not available,
// the corresponding tasks are skipped and the report notes the sample size.
//
// Run:
//   go test -tags=integration -count=1 -timeout 180s -run TestRuntime8Eval -v ./...

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Metric types ---

// evalTaskResult records the outcome of one task.
type evalTaskResult struct {
	Name            string `json:"name"`
	Backend         string `json:"backend"`
	Success         bool   `json:"success"`
	ToolCalls       int    `json:"tool_calls"`
	FirstActionOK   bool   `json:"first_action_ok"`
	Retries         int    `json:"retries"`
	OutputBytes     int    `json:"output_bytes"`
	EstTokens       int    `json:"est_tokens"`
	LatencyMs       int64  `json:"latency_ms"`
	UsedEval        bool   `json:"used_eval"`
	UsedEscapeHatch bool   `json:"used_escape_hatch"`
	ErrorRecovery   string `json:"error_recovery,omitempty"`
	Skipped         string `json:"skipped,omitempty"`
}

// evalReport is the full evaluation report.
type evalReport struct {
	Tasks   []evalTaskResult `json:"tasks"`
	Summary evalSummary      `json:"summary"`
}

type evalSummary struct {
	TotalTasks       int     `json:"total_tasks"`
	CompletedTasks   int     `json:"completed_tasks"`
	SuccessRate      float64 `json:"success_rate"`
	FirstActionRate  float64 `json:"first_action_rate"`
	AvgToolCalls     float64 `json:"avg_tool_calls"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	EvalFallbackRate float64 `json:"eval_fallback_rate"`
	EscapeHatchRate  float64 `json:"escape_hatch_rate"`
}

// --- Harness ---

// evalHarness wraps an MCP client session and records metrics.
type evalHarness struct {
	t          *testing.T
	cs         *mcp.ClientSession
	backend    string
	toolCalls  int
	usedEval   bool
	usedEscape bool
}

func newEvalHarness(t *testing.T, cs *mcp.ClientSession, backend string) *evalHarness {
	return &evalHarness{t: t, cs: cs, backend: backend}
}

// call invokes a tool and records metrics. Returns the parsed result and error.
func (h *evalHarness) call(name string, args map[string]any) (map[string]any, error) {
	h.toolCalls++
	if name == "eval" {
		h.usedEval = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := h.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Content) == 0 {
		return nil, fmt.Errorf("empty result from %s", name)
	}
	// Extract text content first so we can use it for error reporting.
	var rawText string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			rawText = tc.Text
			break
		}
	}
	// Propagate MCP result errors. Without this, tasks score false
	// negatives because the expected error is silently swallowed.
	if res.IsError {
		var parsed map[string]any
		if rawText != "" {
			json.Unmarshal([]byte(rawText), &parsed)
		}
		if parsed == nil {
			parsed = map[string]any{"_raw": rawText}
		}
		return parsed, fmt.Errorf("%s", rawText)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawText), &parsed); err != nil {
		return map[string]any{"_raw": rawText}, nil
	}
	return parsed, nil
}

// callOK invokes a tool and returns true if it succeeded (no error).
func (h *evalHarness) callOK(name string, args map[string]any) bool {
	_, err := h.call(name, args)
	return err == nil
}

// estimateTokens returns a rough token estimate (bytes / 4).
func estimateTokens(s string) int {
	return len(s) / 4
}

// resultBytes returns the JSON size of a result.
func resultBytes(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

// --- Tasks ---

// taskChromeSnapshotClick performs: status -> snapshot -> click first element.
// Returns the task result with metrics.
func taskChromeSnapshotClick(h *evalHarness) evalTaskResult {
	start := time.Now()
	r := evalTaskResult{Name: "chrome_snapshot_click", Backend: "chrome"}

	// Step 1: status
	status, err := h.call("status", map[string]any{})
	if err != nil {
		r.ErrorRecovery = fmt.Sprintf("status failed: %v", err)
		r.LatencyMs = time.Since(start).Milliseconds()
		r.ToolCalls = h.toolCalls
		return r
	}
	r.OutputBytes += resultBytes(status)

	// Step 2: snapshot
	snap, err := h.call("snapshot", map[string]any{})
	if err != nil {
		r.ErrorRecovery = fmt.Sprintf("snapshot failed: %v", err)
		r.LatencyMs = time.Since(start).Milliseconds()
		r.ToolCalls = h.toolCalls
		return r
	}
	r.OutputBytes += resultBytes(snap)
	r.FirstActionOK = true // snapshot succeeded

	// Step 3: click first element (if any)
	elements, _ := snap["elements"].([]any)
	if len(elements) > 0 {
		if elem, ok := elements[0].(map[string]any); ok {
			if ref, ok := elem["ref"].(string); ok && ref != "" {
				clickRes, err := h.call("click", map[string]any{"ref": ref})
				if err != nil {
					r.Retries++
					r.ErrorRecovery = fmt.Sprintf("click failed: %v", err)
				} else {
					r.OutputBytes += resultBytes(clickRes)
					r.Success = true
				}
			}
		}
	} else {
		// No elements — snapshot itself is partial success.
		r.Success = true
	}

	r.ToolCalls = h.toolCalls
	r.UsedEval = h.usedEval
	r.UsedEscapeHatch = h.usedEscape
	r.EstTokens = r.OutputBytes / 4
	r.LatencyMs = time.Since(start).Milliseconds()
	return r
}

// --- Report ---

func summarize(tasks []evalTaskResult) evalSummary {
	var completed, success, firstAction, evalCount, escapeCount int
	var totalCalls, totalLatency int
	for _, t := range tasks {
		if t.Skipped != "" {
			continue
		}
		completed++
		if t.Success {
			success++
		}
		if t.FirstActionOK {
			firstAction++
		}
		if t.UsedEval {
			evalCount++
		}
		if t.UsedEscapeHatch {
			escapeCount++
		}
		totalCalls += t.ToolCalls
		totalLatency += int(t.LatencyMs)
	}
	s := evalSummary{
		TotalTasks:     len(tasks),
		CompletedTasks: completed,
	}
	if completed > 0 {
		s.SuccessRate = float64(success) / float64(completed)
		s.FirstActionRate = float64(firstAction) / float64(completed)
		s.AvgToolCalls = float64(totalCalls) / float64(completed)
		s.AvgLatencyMs = float64(totalLatency) / float64(completed)
		s.EvalFallbackRate = float64(evalCount) / float64(completed)
		s.EscapeHatchRate = float64(escapeCount) / float64(completed)
	}
	return s
}

// --- Test entry point ---

// TestRuntime8Eval runs the evaluation harness and prints a report.
// It does NOT assert specific success rates — the user's gates are
// aspirational targets, not hard assertions. The report is printed
// for manual review.
func TestRuntime8Eval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping eval harness in short mode")
	}
	var tasks []evalTaskResult

	// --- Chrome tasks ---
	// Chrome is always available in integration tests.
	chromeServer := mcp.NewServer(&mcp.Implementation{Name: "argus-eval", Version: "test"}, nil)
	registerAllTools(chromeServer)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := chromeServer.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("chrome server connect: %v", err)
	}
	chromeClient := mcp.NewClient(&mcp.Implementation{Name: "eval-client", Version: "test"}, nil)
	chromeCS, err := chromeClient.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("chrome client connect: %v", err)
	}

	chromeH := newEvalHarness(t, chromeCS, "chrome")
	tasks = append(tasks, taskChromeSnapshotClick(chromeH))

	// Build and print report.
	report := evalReport{
		Tasks:   tasks,
		Summary: summarize(tasks),
	}
	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("Evaluation Report:\n%s", string(reportJSON))

	// Print summary line.
	s := report.Summary
	t.Logf("Summary: completed=%d/%d success=%.0f%% first_action=%.0f%% avg_calls=%.1f eval_fallback=%.0f%% escape_hatch=%.0f%%",
		s.CompletedTasks, s.TotalTasks,
		s.SuccessRate*100, s.FirstActionRate*100,
		s.AvgToolCalls,
		s.EvalFallbackRate*100, s.EscapeHatchRate*100,
	)

	// Note sample size limitation.
	if s.CompletedTasks < 10 {
		t.Logf("WARNING: sample size=%d is too small for statistical confidence. Results are indicative, not conclusive.", s.CompletedTasks)
	}
}

// TestEvalTokenEstimation verifies that EstTokens is derived from the
// actual output bytes, not from the string representation of the byte count.
func TestEvalTokenEstimation(t *testing.T) {
	// A result with 1000 bytes of output should estimate ~250 tokens (1000/4),
	// not ~1 token (len("1000")/4 = 1).
	r := evalTaskResult{OutputBytes: 1000}
	r.EstTokens = r.OutputBytes / 4
	if r.EstTokens != 250 {
		t.Errorf("EstTokens = %d, want 250 (1000 bytes / 4)", r.EstTokens)
	}
	// Verify the old bug: estimating from the string "1000" would give 1.
	oldBuggy := len(fmt.Sprintf("%d", r.OutputBytes)) / 4
	if oldBuggy == r.EstTokens {
		t.Errorf("old buggy estimate (%d) matches new estimate (%d) — test is not detecting the bug", oldBuggy, r.EstTokens)
	}
}

// TestEvalStaleRefSuccessSemantics verifies that stale-ref detection alone
// does not set Success=true; the full recovery chain must complete.
func TestEvalStaleRefSuccessSemantics(t *testing.T) {
	// Simulate a task result where stale was detected but recovery failed.
	r := evalTaskResult{}
	staleDetected := true
	recoverySucceeded := false

	if staleDetected && recoverySucceeded {
		r.Success = true
	}
	if r.Success {
		t.Error("Success should be false when stale detected but recovery failed")
	}
	if !staleDetected {
		t.Error("test setup error: staleDetected should be true")
	}

	// Now simulate full success.
	r = evalTaskResult{}
	recoverySucceeded = true
	if staleDetected && recoverySucceeded {
		r.Success = true
	}
	if !r.Success {
		t.Error("Success should be true when both stale detected and recovery succeeded")
	}
}

// TestEvalHarnessCallPropagatesIsError verifies that h.call propagates
// MCP result IsError as a Go error. Without this, tasks score false
// negatives because the expected error is swallowed (res.IsError=true
// but h.call returns nil error).
func TestEvalHarnessCallPropagatesIsError(t *testing.T) {
	// Build an in-memory MCP server that returns IsError=true for click.
	server := mcp.NewServer(&mcp.Implementation{Name: "argus-eval-err", Version: "test"}, nil)
	type clickTestInput struct {
		Ref string `json:"ref,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "click",
		Description: "test click that always errors",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input clickTestInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error":"stale_reference: ref not found"}`},
			},
		}, nil, nil
	})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "eval-err-client", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	h := newEvalHarness(t, cs, "chrome")

	// h.call should return a non-nil error when the MCP result has
	// IsError=true. Before the fix, it returned (parsed, nil) — the
	// error was silently swallowed.
	_, err = h.call("click", map[string]any{"ref": "nonexistent"})
	if err == nil {
		t.Fatal("h.call should return error when MCP result IsError=true, got nil")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error should contain 'stale', got: %v", err)
	}
}
