package main

import (
	"fmt"
	"testing"
)

func TestNextActionID(t *testing.T) {
	id1 := nextActionID()
	id2 := nextActionID()
	id3 := nextActionID()

	if id1 == id2 || id2 == id3 || id1 == id3 {
		t.Errorf("action IDs not unique: %s, %s, %s", id1, id2, id3)
	}
	if id1 == "" {
		t.Error("action ID is empty")
	}
}

func TestActionIDFormat(t *testing.T) {
	id := ActionID(formatActionID(1))
	if id != "act_0001" {
		t.Errorf("formatActionID(1) = %q, want act_0001", id)
	}
	id = ActionID(formatActionID(42))
	if id != "act_0042" {
		t.Errorf("formatActionID(42) = %q, want act_0042", id)
	}
	id = ActionID(formatActionID(9999))
	if id != "act_9999" {
		t.Errorf("formatActionID(9999) = %q, want act_9999", id)
	}
}

func TestBeginActionReturnsRecord(t *testing.T) {
	rec := BeginAction("test_op")
	if rec == nil {
		t.Fatal("BeginAction returned nil")
	}
	if rec.ID == "" {
		t.Error("record ID is empty")
	}
	if rec.Op != "test_op" {
		t.Errorf("record Op = %q, want test_op", rec.Op)
	}
	if rec.StartTime.IsZero() {
		t.Error("record StartTime is zero")
	}
	if rec.EndTime != nil {
		t.Error("record EndTime should be nil before End()")
	}
}

func TestActionRecordEnd(t *testing.T) {
	rec := BeginAction("test_op")
	rec.End()
	if rec.EndTime == nil {
		t.Error("EndTime should be set after End()")
	}
	if rec.EndTime.Before(rec.StartTime) {
		t.Error("EndTime should be after StartTime")
	}
}

func TestActionRecordSetTarget(t *testing.T) {
	rec := BeginAction("click")
	rec.SetTarget("ABC123")
	if rec.TargetID != "ABC123" {
		t.Errorf("TargetID = %q, want ABC123", rec.TargetID)
	}
}

func TestActionRecordSetError(t *testing.T) {
	rec := BeginAction("navigate")
	err := &ToolError{Code: ErrTimeout, Op: "navigate", Message: "page load timeout"}
	rec.SetError(err)
	if rec.Error == nil {
		t.Fatal("Error should be set after SetError()")
	}
	if rec.Error.Code != ErrTimeout {
		t.Errorf("Error.Code = %q, want timeout", rec.Error.Code)
	}
}

func TestActionRecordNilSafe(t *testing.T) {
	var rec *ActionRecord
	rec.End()          // should not panic
	rec.SetTarget("x") // should not panic
	rec.SetError(nil)  // should not panic
}

func TestBeginActionNoGlobalState(t *testing.T) {
	// Two interleaved actions should not interfere with each other.
	// There is no global currentAction — each record is independent.
	rec1 := BeginAction("click")
	rec2 := BeginAction("navigate")

	if rec1.ID == rec2.ID {
		t.Fatal("interleaved actions got same ID")
	}

	// End rec2 first (interleaved) — should not affect rec1
	rec2.End()

	if rec1.EndTime != nil {
		t.Error("rec1.EndTime should still be nil — rec2.End() must not affect rec1")
	}
	if rec2.EndTime == nil {
		t.Error("rec2.EndTime should be set after End()")
	}

	// Now end rec1
	rec1.End()
	if rec1.EndTime == nil {
		t.Error("rec1.EndTime should be set after End()")
	}
}

func TestActionRecordSetParams(t *testing.T) {
	rec := &ActionRecord{ID: "act_test", Op: "click"}
	rec.SetParams(map[string]any{"x": 100, "y": 200})
	if rec.Params == nil {
		t.Fatal("Params should be set")
	}
	// Small params should fit without truncation
	m, ok := rec.Params.(map[string]any)
	if !ok {
		t.Fatalf("Params should be a map, got %T", rec.Params)
	}
	if m["x"] != 100 {
		t.Errorf("params x = %v, want 100", m["x"])
	}
}

func TestActionRecordSetParamsTruncation(t *testing.T) {
	rec := &ActionRecord{ID: "act_test", Op: "fill"}
	// Create params larger than maxParamSummaryLen (512 bytes)
	large := map[string]any{}
	for i := 0; i < 100; i++ {
		large[formatInt(i)] = "this is a long value that will exceed the limit when serialized"
	}
	rec.SetParams(large)
	s, ok := rec.Params.(string)
	if !ok {
		t.Fatalf("truncated params should be a string, got %T", rec.Params)
	}
	if len(s) <= maxParamSummaryLen {
		t.Errorf("truncated params len = %d, should be > %d", len(s), maxParamSummaryLen)
	}
}

func TestActionRecordSetResult(t *testing.T) {
	rec := &ActionRecord{ID: "act_test", Op: "navigate"}
	rec.SetResult(map[string]any{"url": "http://example.com"})
	if rec.Result == nil {
		t.Fatal("Result should be set")
	}
}

func TestActionRecordSetErrorFromError(t *testing.T) {
	rec := &ActionRecord{ID: "act_test", Op: "click"}
	rec.SetErrorFromError(fmt.Errorf("element not found"))
	if rec.Error == nil {
		t.Fatal("Error should be set")
	}
	if rec.Error.Message != "element not found" {
		t.Errorf("Error.Message = %q, want 'element not found'", rec.Error.Message)
	}
}

func TestActionRecordSetErrorFromErrorNil(t *testing.T) {
	rec := &ActionRecord{ID: "act_test", Op: "click"}
	rec.SetErrorFromError(nil)
	if rec.Error != nil {
		t.Error("Error should not be set for nil error")
	}
}

func TestActionRecordSetParamsNil(t *testing.T) {
	rec := &ActionRecord{ID: "act_test", Op: "click"}
	rec.SetParams(nil)
	if rec.Params != nil {
		t.Error("Params should be nil for nil input")
	}
}

func TestActionRecordSetResultNilSafe(t *testing.T) {
	rec := &ActionRecord{ID: "act_test", Op: "click"}
	rec.SetResult(nil)
	if rec.Result != nil {
		t.Error("Result should be nil for nil input")
	}
}
