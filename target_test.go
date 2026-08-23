package main

import (
	"testing"
)

func TestTargetSessionSetAndGet(t *testing.T) {
	// Save and restore
	targetSessionMu.Lock()
	orig := currentTargetID
	currentTargetID = ""
	targetSessionMu.Unlock()
	defer func() {
		targetSessionMu.Lock()
		currentTargetID = orig
		targetSessionMu.Unlock()
	}()

	if id := getCurrentTargetID(); id != "" {
		t.Errorf("initial targetID = %q, want empty", id)
	}

	setCurrentTargetID("ABC123")
	if id := getCurrentTargetID(); id != "ABC123" {
		t.Errorf("after set, targetID = %q, want ABC123", id)
	}

	setCurrentTargetID("XYZ789")
	if id := getCurrentTargetID(); id != "XYZ789" {
		t.Errorf("after second set, targetID = %q, want XYZ789", id)
	}
}

func TestTargetSessionClear(t *testing.T) {
	targetSessionMu.Lock()
	orig := currentTargetID
	currentTargetID = "TEST123"
	targetSessionMu.Unlock()
	defer func() {
		targetSessionMu.Lock()
		currentTargetID = orig
		targetSessionMu.Unlock()
	}()

	clearCurrentTargetID()
	if id := getCurrentTargetID(); id != "" {
		t.Errorf("after clear, targetID = %q, want empty", id)
	}
}

func TestFindTargetByID(t *testing.T) {
	// Test with mock targets list
	targets := []browserTarget{
		{ID: "tab1", Type: "page", URL: "http://localhost:3000/page1"},
		{ID: "tab2", Type: "page", URL: "http://localhost:3000/page2"},
		{ID: "tab3", Type: "page", URL: "http://localhost:3000/page1"}, // same URL as tab1
	}

	// Find by ID should return exact match
	found := findTargetByID(targets, "tab2")
	if found == nil {
		t.Fatal("findTargetByID returned nil for tab2")
	}
	if found.ID != "tab2" {
		t.Errorf("found.ID = %q, want tab2", found.ID)
	}

	// Non-existent ID
	notFound := findTargetByID(targets, "nonexistent")
	if notFound != nil {
		t.Errorf("findTargetByID for nonexistent returned %v, want nil", notFound)
	}
}

func TestFindTargetByURLAmbiguous(t *testing.T) {
	// Two tabs with same URL — URL-based lookup is ambiguous
	targets := []browserTarget{
		{ID: "tab1", Type: "page", URL: "http://localhost:3000/page1"},
		{ID: "tab2", Type: "page", URL: "http://localhost:3000/page1"},
	}

	// URL match returns the first one (documented behavior)
	found := findTargetByURL(targets, "http://localhost:3000/page1")
	if found == nil {
		t.Fatal("findTargetByURL returned nil")
	}
	// This is the ambiguity — we can't know which one the agent meant
	// This test documents that URL-based lookup is unreliable for same-URL tabs
	t.Logf("URL match returned %s (ambiguity: could be tab1 or tab2)", found.ID)
}
