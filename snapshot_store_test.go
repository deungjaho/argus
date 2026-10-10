package main

import (
	"strings"
	"testing"
	"time"
)

// helper: reset the global snapshot store, return restore func
func withCleanSnapshotStore() func() {
	snapMu.Lock()
	defer snapMu.Unlock()
	origStore, origOrder := snapStore, snapOrder
	snapStore = map[string]*storedSnapshot{}
	snapOrder = nil
	return func() {
		snapMu.Lock()
		defer snapMu.Unlock()
		snapStore, snapOrder = origStore, origOrder
	}
}

func snapshotElements(refs ...string) []any {
	els := make([]any, len(refs))
	for i, r := range refs {
		els[i] = map[string]any{
			"ref":  r,
			"m":    "m" + r,
			"tag":  "button",
			"text": "btn " + r,
			"rect": map[string]any{"x": float64(i), "y": 0, "width": 10, "height": 10},
		}
	}
	return els
}

func TestStoreSnapshotRoundTrip(t *testing.T) {
	defer withCleanSnapshotStore()()

	snap := storeSnapshot("ws://127.0.0.1:9224/devtools/page/T1", "http://x", snapshotElements("e1", "e2"))
	if snap.ID == "" {
		t.Fatal("snapshot ID empty")
	}
	if snap.TargetID != "T1" {
		t.Errorf("TargetID = %q, want T1", snap.TargetID)
	}
	if len(snap.Elements) != 2 {
		t.Fatalf("elements = %d, want 2", len(snap.Elements))
	}
	if snap.Elements["e1"].Mark != "me1" {
		t.Errorf("e1 mark = %q, want me1", snap.Elements["e1"].Mark)
	}

	got, ok := getSnapshot(snap.ID)
	if !ok || got.ID != snap.ID {
		t.Fatalf("getSnapshot(%q) not found", snap.ID)
	}
}

func TestStoreSnapshotStripsInternalMark(t *testing.T) {
	defer withCleanSnapshotStore()()

	raw := snapshotElements("e1")
	mark := raw[0].(map[string]any)["m"]
	storeSnapshot("ws://127.0.0.1:9224/devtools/page/T1", "http://x", raw)
	if _, leaked := raw[0].(map[string]any)["m"]; leaked {
		t.Errorf("internal mark %v leaked into agent-facing element", mark)
	}
}

func TestStoreSnapshotEvictsOldest(t *testing.T) {
	defer withCleanSnapshotStore()()

	ids := make([]string, 0, maxStoredSnapshots+2)
	for i := 0; i < maxStoredSnapshots+2; i++ {
		snap := storeSnapshot("ws://127.0.0.1:9224/devtools/page/T1", "http://x", snapshotElements("e1"))
		ids = append(ids, snap.ID)
	}
	if _, ok := getSnapshot(ids[0]); ok {
		t.Error("oldest snapshot should be evicted")
	}
	if _, ok := getSnapshot(ids[1]); ok {
		t.Error("second-oldest snapshot should be evicted")
	}
	if _, ok := getSnapshot(ids[len(ids)-1]); !ok {
		t.Error("newest snapshot should be retained")
	}
}

func TestLatestSnapshotPerTarget(t *testing.T) {
	defer withCleanSnapshotStore()()

	old := storeSnapshot("ws://127.0.0.1:9224/devtools/page/T1", "http://a", snapshotElements("e1"))
	newerOther := storeSnapshot("ws://127.0.0.1:9224/devtools/page/T2", "http://b", snapshotElements("e1"))
	latest := storeSnapshot("ws://127.0.0.1:9224/devtools/page/T1", "http://a2", snapshotElements("e1"))

	if got := latestSnapshot("T1"); got.ID != latest.ID {
		t.Errorf("latestSnapshot(T1) = %s, want %s", got.ID, latest.ID)
	}
	if got := latestSnapshot("T2"); got.ID != newerOther.ID {
		t.Errorf("latestSnapshot(T2) = %s, want %s", got.ID, newerOther.ID)
	}
	if got := latestSnapshot("T9"); got != nil {
		t.Errorf("latestSnapshot(unknown) = %v, want nil", got)
	}
	if old.ID == latest.ID {
		t.Error("snapshot IDs must be distinct")
	}
}

func TestResolveSnapshotMark(t *testing.T) {
	defer withCleanSnapshotStore()()

	snap := storeSnapshot("ws://127.0.0.1:9224/devtools/page/T1", "http://x", snapshotElements("e1"))

	mark, err := resolveSnapshotMark(snap.ID, "e1")
	if err != nil {
		t.Fatalf("resolveSnapshotMark: %v", err)
	}
	if mark != "me1" {
		t.Errorf("mark = %q, want me1", mark)
	}

	if _, err := resolveSnapshotMark("snap_9999", "e1"); err == nil {
		t.Error("expected error for unknown snapshot")
	}
	if _, err := resolveSnapshotMark(snap.ID, "missing"); err == nil {
		t.Error("expected error for ref not in snapshot")
	} else if !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("error should tell agent to re-snapshot, got %q", err.Error())
	}
}

func TestEvidenceSinceFilters(t *testing.T) {
	restore := withTestRecorder(50, 50)
	defer restore()

	rec.RecordEvent("console", "tab1", map[string]any{"level": "log", "text": "noise"})
	before := time.Now()
	time.Sleep(10 * time.Millisecond)
	rec.RecordEvent("console", "tab1", map[string]any{"level": "error", "text": "boom"})
	rec.RecordEvent("exception", "tab1", map[string]any{"text": "uncaught"})
	rec.RecordEvent("console", "tab2", map[string]any{"level": "error", "text": "other tab"})
	rec.RecordEvent("network", "tab1", NetworkEvidence{Phase: "request", RequestID: "r1", URL: "http://api/x", Method: "GET"})
	rec.RecordEvent("network", "tab1", NetworkEvidence{Phase: "response", RequestID: "r1", URL: "http://api/x", Status: 500})

	ev := rec.EvidenceSince("tab1", before)
	if ev == nil {
		t.Fatal("expected evidence, got nil")
	}
	console, _ := ev["console"].(map[string]any)
	if console == nil {
		t.Fatal("expected console evidence")
	}
	items, _ := console["items"].([]map[string]any)
	if len(items) != 2 {
		t.Fatalf("console items = %d, want 2 (error + exception)", len(items))
	}
	for _, c := range items {
		if c["text"] == "other tab" {
			t.Error("evidence leaked event from another target")
		}
		if c["text"] == "noise" {
			t.Error("info-level console should be filtered out")
		}
	}
	net, _ := ev["network_failures"].(map[string]any)
	if net == nil || net["count"] != 1 {
		t.Errorf("expected 1 network failure, got %v", net)
	}
}

func TestEvidenceSinceEmpty(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	rec.RecordEvent("console", "tab1", map[string]any{"level": "log", "text": "fine"})
	if ev := rec.EvidenceSince("tab1", time.Now().Add(-time.Second)); ev != nil {
		t.Errorf("expected nil evidence for quiet window, got %v", ev)
	}
}
