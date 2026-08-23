package main

// automator_refs.go — bounded backend-specific ref registry for WeChat.
//
// A ref is an opaque string that encodes the identity of a page element
// observed in a snapshot. It records:
//   - page ID (int)
//   - element ID (string)
//   - optional node/video ID
//   - tag name
//   - snapshot generation (increments on each snapshot)
//   - connection epoch (increments on reconnect)
//
// Refs are invalidated on:
//   - route/page change (page ID differs)
//   - reconnect (epoch differs)
//
// A stale ref returns ErrStaleReference, never silently re-matches another
// element. This is the core safety property: the agent cannot accidentally
// tap the wrong element after navigation.
//
// The registry is bounded: it stores at most maxWxRefs entries, evicting
// the oldest. It does not cache unbounded element/page objects.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

const maxWxRefs = 512

// wxRefEntry is one stored ref in the registry.
type wxRefEntry struct {
	Ref       string `json:"ref"`
	PageID    int    `json:"page_id"`
	ElementID string `json:"element_id"`
	NodeID    string `json:"node_id,omitempty"`
	VideoID   string `json:"video_id,omitempty"`
	TagName   string `json:"tag_name"`
	Snapshot  int64  `json:"snapshot"`
	Epoch     int    `json:"epoch"`
}

// wxRefRegistry is the bounded ref registry.
type wxRefRegistry struct {
	mu       sync.Mutex
	entries  map[string]wxRefEntry
	order    []string // FIFO order for eviction
	snapshot int64    // monotonic snapshot generation
}

var wxRefs = &wxRefRegistry{
	entries: make(map[string]wxRefEntry, maxWxRefs),
}

// nextSnapshotGeneration increments and returns the snapshot generation.
func (r *wxRefRegistry) nextSnapshotGeneration() int64 {
	return atomic.AddInt64(&r.snapshot, 1)
}

// currentEpoch returns the current automator connection epoch.
func currentWxEpoch() int {
	automatorPoolMu.Lock()
	defer automatorPoolMu.Unlock()
	if automatorConn_ != nil {
		return automatorConn_.epoch
	}
	return automatorEpoch
}

// register creates a ref for the given element on the given page and
// stores it in the registry. Returns the opaque ref string.
func (r *wxRefRegistry) register(pageID int, elem wxElementRef, snapshot int64) string {
	epoch := currentWxEpoch()
	ref := encodeWxRef(pageID, elem.ElementID, snapshot, epoch)
	entry := wxRefEntry{
		Ref:       ref,
		PageID:    pageID,
		ElementID: elem.ElementID,
		NodeID:    elem.NodeID,
		VideoID:   elem.VideoID,
		TagName:   elem.TagName,
		Snapshot:  snapshot,
		Epoch:     epoch,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[ref]; !exists {
		r.order = append(r.order, ref)
		// Evict oldest if over capacity.
		for len(r.order) > maxWxRefs {
			oldest := r.order[0]
			delete(r.entries, oldest)
			r.order = r.order[1:]
		}
	}
	r.entries[ref] = entry
	return ref
}

// resolve looks up a ref and validates it against the current page and
// connection epoch. Returns the stored entry and nil error if valid.
// Returns ErrStaleReference if the ref is from a different page, epoch,
// or is not found.
func (r *wxRefRegistry) resolve(ref string, currentPageID int) (wxRefEntry, error) {
	r.mu.Lock()
	entry, ok := r.entries[ref]
	r.mu.Unlock()
	if !ok {
		return wxRefEntry{}, &ToolError{
			Code:    ErrStaleReference,
			Op:      "resolve_ref",
			Message: fmt.Sprintf("ref %q not found in registry", ref),
		}
	}
	// Check epoch — if the connection reconnected, all refs are stale.
	currentEpoch := currentWxEpoch()
	if entry.Epoch != currentEpoch {
		return wxRefEntry{}, &ToolError{
			Code:    ErrStaleReference,
			Op:      "resolve_ref",
			Message: fmt.Sprintf("ref %q is from epoch %d, current epoch is %d (connection reconnected)", ref, entry.Epoch, currentEpoch),
		}
	}
	// Check page ID — if the page changed, refs from the old page are stale.
	if currentPageID > 0 && entry.PageID != currentPageID {
		return wxRefEntry{}, &ToolError{
			Code:    ErrStaleReference,
			Op:      "resolve_ref",
			Message: fmt.Sprintf("ref %q is from page %d, current page is %d (page changed)", ref, entry.PageID, currentPageID),
		}
	}
	return entry, nil
}

// invalidatePage removes all refs for the given page ID. Called on
// navigation/page change.
func (r *wxRefRegistry) invalidatePage(pageID int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	newOrder := r.order[:0]
	for _, ref := range r.order {
		entry, ok := r.entries[ref]
		if ok && entry.PageID == pageID {
			delete(r.entries, ref)
			continue
		}
		newOrder = append(newOrder, ref)
	}
	r.order = newOrder
}

// invalidateAll removes all refs. Called on reconnect or backend switch.
func (r *wxRefRegistry) invalidateAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = make(map[string]wxRefEntry, maxWxRefs)
	r.order = nil
}

// encodeWxRef creates an opaque ref string. The ref is a base64-encoded
// JSON payload containing the page ID, element ID, snapshot, and epoch.
// It is opaque to the agent — the agent passes it back verbatim.
func encodeWxRef(pageID int, elementID string, snapshot int64, epoch int) string {
	payload, _ := json.Marshal([]any{pageID, elementID, snapshot, epoch})
	return "wx:" + base64.RawURLEncoding.EncodeToString(payload)
}

// decodeWxRef parses an opaque ref string. Returns the components.
// This is used only for diagnostics; resolution goes through the registry.
func decodeWxRef(ref string) (pageID int, elementID string, snapshot int64, epoch int, ok bool) {
	if !strings.HasPrefix(ref, "wx:") {
		return 0, "", 0, 0, false
	}
	data, err := base64.RawURLEncoding.DecodeString(ref[3:])
	if err != nil {
		return 0, "", 0, 0, false
	}
	var parts []any
	if err := json.Unmarshal(data, &parts); err != nil || len(parts) < 4 {
		return 0, "", 0, 0, false
	}
	pid, _ := numberValue(parts[0])
	eid, _ := parts[1].(string)
	snap, _ := numberValue(parts[2])
	ep, _ := numberValue(parts[3])
	return int(pid), eid, int64(snap), int(ep), true
}

// --- High-level ref operations ---

// resolveRefOrSelector resolves a ref or selector to a page ID and element ref.
// If ref is a registered wx: ref, it is resolved and validated against the
// current page. If ref is a plain selector, it is resolved via Page.getElement.
// Returns the page ID, element ref, and nil error on success.
func resolveRefOrSelector(refOrSelector string) (int, wxElementRef, error) {
	pageID, err := resolveCurrentPageID()
	if err != nil {
		return 0, wxElementRef{}, err
	}
	if strings.HasPrefix(refOrSelector, "wx:") {
		entry, err := wxRefs.resolve(refOrSelector, pageID)
		if err != nil {
			return 0, wxElementRef{}, err
		}
		return entry.PageID, wxElementRef{
			ElementID: entry.ElementID,
			TagName:   entry.TagName,
			NodeID:    entry.NodeID,
			VideoID:   entry.VideoID,
		}, nil
	}
	// Plain selector — resolve via Page.getElement.
	elem, err := automatorGetElement(pageID, refOrSelector)
	if err != nil {
		return 0, wxElementRef{}, err
	}
	return pageID, elem, nil
}
