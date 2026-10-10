package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// --- Stored snapshots: stable refs + diff base ---
//
// snapshot/look tag every interactive element with a node-stable mark
// (data-argus-m, assigned once per DOM node — re-created nodes get a new
// mark). The ref→mark map is stored here under a snapshot_id.
//
// This powers two features:
//   - Stale ref detection: click/fill with snapshot=<id> verifies the
//     resolved element still carries the recorded mark; a mismatch means
//     the SPA re-rendered and the ref points at a different node.
//   - diff_snapshot: compare the stored element list against a fresh
//     snapshot to report added/removed/changed/recreated elements.

const (
	maxStoredSnapshots = 8
	elementMarkAttr    = "data-argus-m"
)

// errStaleRef marks "element re-created since snapshot" errors so
// autoWait can fail fast instead of polling until timeout.
var errStaleRef = errors.New("stale element reference")

var (
	snapMu      sync.Mutex
	snapStore   = map[string]*storedSnapshot{}
	snapOrder   []string // oldest → newest, for eviction
	snapCounter uint64
)

type snapshotElement struct {
	Ref  string
	Mark string
	Tag  string
	Text string
	Rect map[string]any
}

type storedSnapshot struct {
	ID       string
	TargetID string
	URL      string
	At       time.Time
	Elements map[string]snapshotElement // ref → element
}

func nextSnapshotID() string {
	return "snap_" + formatUintPadded(atomic.AddUint64(&snapCounter, 1), 4)
}

// storeSnapshot records a snapshot's interactive element list under a
// fresh ID. elements are the raw "interactive" entries from the snapshot
// expression output; the internal "m" mark is extracted and stripped
// from the agent-facing payload.
func storeSnapshot(wsURL, url string, elements []any) *storedSnapshot {
	snap := &storedSnapshot{
		ID:       nextSnapshotID(),
		TargetID: targetIDFromWSURL(wsURL),
		URL:      url,
		At:       time.Now(),
		Elements: map[string]snapshotElement{},
	}
	for _, raw := range elements {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ref, _ := m["ref"].(string)
		if ref == "" {
			continue
		}
		se := snapshotElement{Ref: ref}
		se.Mark, _ = m["m"].(string)
		se.Tag, _ = m["tag"].(string)
		se.Text, _ = m["text"].(string)
		se.Rect, _ = m["rect"].(map[string]any)
		snap.Elements[ref] = se
		delete(m, "m")
	}
	snapMu.Lock()
	defer snapMu.Unlock()
	snapStore[snap.ID] = snap
	snapOrder = append(snapOrder, snap.ID)
	for len(snapOrder) > maxStoredSnapshots {
		delete(snapStore, snapOrder[0])
		snapOrder = snapOrder[1:]
	}
	return snap
}

func getSnapshot(id string) (*storedSnapshot, bool) {
	snapMu.Lock()
	defer snapMu.Unlock()
	s, ok := snapStore[id]
	return s, ok
}

// latestSnapshot returns the most recent stored snapshot for targetID,
// or nil if none.
func latestSnapshot(targetID string) *storedSnapshot {
	snapMu.Lock()
	defer snapMu.Unlock()
	for i := len(snapOrder) - 1; i >= 0; i-- {
		if s := snapStore[snapOrder[i]]; s != nil && s.TargetID == targetID {
			return s
		}
	}
	return nil
}

// resolveSnapshotMark looks up the expected element mark for ref in the
// snapshot. Returns the mark to enforce, or an error when the ref is not
// part of the snapshot.
func resolveSnapshotMark(snapID, ref string) (string, error) {
	snap, ok := getSnapshot(snapID)
	if !ok {
		return "", fmt.Errorf("snapshot %s not found (expired or from another run); call snapshot or look again", snapID)
	}
	el, ok := snap.Elements[ref]
	if !ok {
		return "", fmt.Errorf("ref %q is not in snapshot %s; call snapshot or look again for fresh refs", ref, snapID)
	}
	return el.Mark, nil
}
