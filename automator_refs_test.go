package main

// automator_refs_test.go — tests for the WeChat ref registry, route
// invalidation, stale ref detection, and epoch invalidation.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWxRefRegisterAndResolve asserts that a ref can be registered and
// resolved against the same page ID and epoch.
func TestWxRefRegisterAndResolve(t *testing.T) {
	wxRefs.invalidateAll()
	defer wxRefs.invalidateAll()

	// Force a known epoch by setting up the pool state.
	automatorPoolMu.Lock()
	automatorEpoch = 1
	automatorConn_ = &automatorConn{epoch: 1}
	automatorPoolMu.Unlock()
	defer func() {
		automatorPoolMu.Lock()
		automatorConn_ = nil
		automatorPoolMu.Unlock()
	}()

	elem := wxElementRef{ElementID: "elem-1", TagName: "button"}
	snapGen := wxRefs.nextSnapshotGeneration()
	ref := wxRefs.register(1, elem, snapGen)

	if ref == "" {
		t.Fatal("register returned empty ref")
	}
	if !strings.HasPrefix(ref, "wx:") {
		t.Errorf("ref should start with 'wx:', got %q", ref)
	}

	entry, err := wxRefs.resolve(ref, 1)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if entry.ElementID != "elem-1" {
		t.Errorf("elementId = %q, want %q", entry.ElementID, "elem-1")
	}
	if entry.PageID != 1 {
		t.Errorf("pageId = %d, want 1", entry.PageID)
	}
}

// TestWxRefStaleOnPageChange asserts that a ref from page 1 returns
// ErrStaleReference when resolved against page 2.
func TestWxRefStaleOnPageChange(t *testing.T) {
	wxRefs.invalidateAll()
	defer wxRefs.invalidateAll()

	automatorPoolMu.Lock()
	automatorEpoch = 1
	automatorConn_ = &automatorConn{epoch: 1}
	automatorPoolMu.Unlock()
	defer func() {
		automatorPoolMu.Lock()
		automatorConn_ = nil
		automatorPoolMu.Unlock()
	}()

	elem := wxElementRef{ElementID: "elem-1", TagName: "button"}
	snapGen := wxRefs.nextSnapshotGeneration()
	ref := wxRefs.register(1, elem, snapGen)

	// Resolve against a different page ID.
	_, err := wxRefs.resolve(ref, 2)
	if err == nil {
		t.Fatal("expected stale reference error, got nil")
	}
	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != ErrStaleReference {
		t.Errorf("error code = %s, want %s", te.Code, ErrStaleReference)
	}
}

// TestWxRefStaleOnEpochChange asserts that a ref from epoch 1 returns
// ErrStaleReference when the connection epoch changes (reconnect).
func TestWxRefStaleOnEpochChange(t *testing.T) {
	wxRefs.invalidateAll()
	defer wxRefs.invalidateAll()

	automatorPoolMu.Lock()
	automatorEpoch = 1
	automatorConn_ = &automatorConn{epoch: 1}
	automatorPoolMu.Unlock()

	elem := wxElementRef{ElementID: "elem-1", TagName: "button"}
	snapGen := wxRefs.nextSnapshotGeneration()
	ref := wxRefs.register(1, elem, snapGen)

	// Simulate reconnect: epoch changes.
	automatorPoolMu.Lock()
	automatorEpoch = 2
	automatorConn_ = &automatorConn{epoch: 2}
	automatorPoolMu.Unlock()
	defer func() {
		automatorPoolMu.Lock()
		automatorConn_ = nil
		automatorPoolMu.Unlock()
	}()

	_, err := wxRefs.resolve(ref, 1)
	if err == nil {
		t.Fatal("expected stale reference error after reconnect, got nil")
	}
	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != ErrStaleReference {
		t.Errorf("error code = %s, want %s", te.Code, ErrStaleReference)
	}
}

// TestWxRefNotFound asserts that an unknown ref returns ErrStaleReference.
func TestWxRefNotFound(t *testing.T) {
	wxRefs.invalidateAll()
	defer wxRefs.invalidateAll()

	automatorPoolMu.Lock()
	automatorEpoch = 1
	automatorConn_ = &automatorConn{epoch: 1}
	automatorPoolMu.Unlock()
	defer func() {
		automatorPoolMu.Lock()
		automatorConn_ = nil
		automatorPoolMu.Unlock()
	}()

	_, err := wxRefs.resolve("wx:nonexistent", 1)
	if err == nil {
		t.Fatal("expected error for unknown ref, got nil")
	}
	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if te.Code != ErrStaleReference {
		t.Errorf("error code = %s, want %s", te.Code, ErrStaleReference)
	}
}

// TestWxRefInvalidatePage asserts that invalidatePage removes refs for
// the given page but leaves refs for other pages intact.
func TestWxRefInvalidatePage(t *testing.T) {
	wxRefs.invalidateAll()
	defer wxRefs.invalidateAll()

	automatorPoolMu.Lock()
	automatorEpoch = 1
	automatorConn_ = &automatorConn{epoch: 1}
	automatorPoolMu.Unlock()
	defer func() {
		automatorPoolMu.Lock()
		automatorConn_ = nil
		automatorPoolMu.Unlock()
	}()

	snapGen := wxRefs.nextSnapshotGeneration()
	ref1 := wxRefs.register(1, wxElementRef{ElementID: "e1", TagName: "button"}, snapGen)
	ref2 := wxRefs.register(2, wxElementRef{ElementID: "e2", TagName: "input"}, snapGen)

	// Invalidate page 1.
	wxRefs.invalidatePage(1)

	// ref1 should be stale.
	_, err := wxRefs.resolve(ref1, 1)
	if err == nil {
		t.Error("ref1 should be stale after invalidatePage(1)")
	}
	// ref2 should still be valid.
	_, err = wxRefs.resolve(ref2, 2)
	if err != nil {
		t.Errorf("ref2 should still be valid after invalidatePage(1), got: %v", err)
	}
}

// TestWxRefRegistryBound asserts that the registry evicts old entries
// when over capacity.
func TestWxRefRegistryBound(t *testing.T) {
	wxRefs.invalidateAll()
	defer wxRefs.invalidateAll()

	automatorPoolMu.Lock()
	automatorEpoch = 1
	automatorConn_ = &automatorConn{epoch: 1}
	automatorPoolMu.Unlock()
	defer func() {
		automatorPoolMu.Lock()
		automatorConn_ = nil
		automatorPoolMu.Unlock()
	}()

	snapGen := wxRefs.nextSnapshotGeneration()
	// Register maxWxRefs + 10 entries.
	firstRef := ""
	for i := 0; i < maxWxRefs+10; i++ {
		ref := wxRefs.register(1, wxElementRef{
			ElementID: "e" + itoa(i),
			TagName:   "view",
		}, snapGen)
		if i == 0 {
			firstRef = ref
		}
	}

	// The first ref should have been evicted.
	_, err := wxRefs.resolve(firstRef, 1)
	if err == nil {
		t.Error("first ref should have been evicted (registry over capacity)")
	}

	// The registry should not exceed maxWxRefs.
	wxRefs.mu.Lock()
	count := len(wxRefs.entries)
	wxRefs.mu.Unlock()
	if count > maxWxRefs {
		t.Errorf("registry has %d entries, max %d", count, maxWxRefs)
	}
}

// TestWxRefDecode asserts that decodeWxRef round-trips the components.
func TestWxRefDecode(t *testing.T) {
	ref := encodeWxRef(42, "elem-abc", 7, 3)
	pageID, elemID, snap, epoch, ok := decodeWxRef(ref)
	if !ok {
		t.Fatalf("decodeWxRef failed for %q", ref)
	}
	if pageID != 42 {
		t.Errorf("pageID = %d, want 42", pageID)
	}
	if elemID != "elem-abc" {
		t.Errorf("elementID = %q, want %q", elemID, "elem-abc")
	}
	if snap != 7 {
		t.Errorf("snapshot = %d, want 7", snap)
	}
	if epoch != 3 {
		t.Errorf("epoch = %d, want 3", epoch)
	}
}

// TestWxRefDecodeInvalid asserts that decodeWxRef rejects non-wx refs.
func TestWxRefDecodeInvalid(t *testing.T) {
	_, _, _, _, ok := decodeWxRef("not-a-ref")
	if ok {
		t.Error("decodeWxRef should return false for non-wx ref")
	}
	_, _, _, _, ok = decodeWxRef("wx:invalid-base64")
	if ok {
		t.Error("decodeWxRef should return false for invalid base64")
	}
}

// --- Helpers ---

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// Ensure json import is used (for potential future test extensions).
var _ = json.Marshal
