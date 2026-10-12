package main

import (
	"fmt"
	"strings"
	"sync"
)

// --- Target session management ---
//
// targetID is the stable primary key for the current tab.
// URL is just a property that may change (SPA navigation).
//
// requireTarget logic:
//  1. If currentTargetID is set, look up by ID. If found, use it.
//     If not found (tab closed or Chrome restarted), clear it
//     and fall back to URL lookup or error.
//  2. If currentTargetID is empty, fall back to URL-based lookup
//     (for backward compatibility with configure-based workflows).
//  3. When a new tab is created or selected, update currentTargetID.

var (
	targetSessionMu sync.RWMutex
	currentTargetID string
)

func getCurrentTargetID() string {
	targetSessionMu.RLock()
	defer targetSessionMu.RUnlock()
	return currentTargetID
}

func setCurrentTargetID(id string) {
	targetSessionMu.Lock()
	defer targetSessionMu.Unlock()
	currentTargetID = id
}

func clearCurrentTargetID() {
	targetSessionMu.Lock()
	defer targetSessionMu.Unlock()
	currentTargetID = ""
}

// findTargetByID returns the target with the given ID, or nil if not found.
// Only returns page-type targets with no parent (top-level tabs).
func findTargetByID(targets []browserTarget, id string) *browserTarget {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	for _, t := range targets {
		if t.ID == id && t.Type == "page" && strings.TrimSpace(t.ParentID) == "" {
			copy := t
			return &copy
		}
	}
	return nil
}

// findTargetByURL returns the first page-type target whose URL starts with targetURL.
// This is ambiguous when multiple tabs have the same URL — prefer findTargetByID.
func findTargetByURL(targets []browserTarget, targetURL string) *browserTarget {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil
	}
	for _, t := range targets {
		if t.Type != "page" || strings.TrimSpace(t.ParentID) != "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(t.URL), targetURL) {
			copy := t
			return &copy
		}
	}
	return nil
}

// resolveTarget finds the target, preferring explicitID, then targetID over URL.
// When explicitID is provided, it does NOT mutate currentTargetID, ensuring concurrency safety.
// If targetID is set but the target no longer exists, returns an error
// (does NOT silently switch to another tab with the same URL).
func resolveTarget(explicitID ...string) (*browserTarget, error) {
	var targetID string
	if len(explicitID) > 0 {
		targetID = strings.TrimSpace(explicitID[0])
	}

	url, _, attach := getConfig()
	url = strings.TrimSpace(url)

	if _, err := cdpVersion(); err != nil {
		if attach {
			return nil, fmt.Errorf("CDP endpoint not reachable at %s (attach mode): %w", cdpBaseURL(), err)
		}
		if err := ensureChromeRunning(); err != nil {
			return nil, fmt.Errorf("Chrome not running and failed to launch: %w", err)
		}
	}

	targets, err := cdpTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to list targets: %w", err)
	}

	// 0. Explicit targetID given (isolated lookup, no mutation of global active target)
	if targetID != "" {
		if target := findTargetByID(targets, targetID); target != nil {
			if target.URL != "" {
				rec.SetCurrentURL(target.ID, target.URL)
			}
			return target, nil
		}
		return nil, fmt.Errorf("target %s not found (tab closed or invalid ID)", targetID)
	}

	// 1. Try targetID first (stable identity)
	if id := getCurrentTargetID(); id != "" {
		if target := findTargetByID(targets, id); target != nil {
			if target.URL != "" {
				rec.SetCurrentURL(target.ID, target.URL)
			}
			return target, nil
		}
		// Target disappeared — clear stale ID
		clearCurrentTargetID()
		// Don't silently switch; return error if we had an explicit target
		return nil, fmt.Errorf("target %s no longer exists (tab closed or Chrome restarted); call select_tab or open to choose a new target", id)
	}

	// 2. Fall back to URL-based lookup (backward compat)
	if url == "" {
		return nil, fmt.Errorf("no target selected and no URL configured; call configure first with {\"url\": \"http://localhost:PORT\"}")
	}
	if target := findTargetByURL(targets, url); target != nil {
		setCurrentTargetID(target.ID)
		if target.URL != "" {
			rec.SetCurrentURL(target.ID, target.URL)
		}
		return target, nil
	}

	// 3. No matching tab — create one (only in launch mode)
	if attach {
		return nil, fmt.Errorf("no tab matching %s in attach mode (open a tab manually or use new_tab)", url)
	}
	version, err := cdpVersion()
	if err != nil {
		return nil, err
	}
	tabID, err := cdpCreateTarget(version.WebSocketDebuggerURL, url, !wantFocus())
	if err != nil {
		return nil, fmt.Errorf("failed to create tab: %w", err)
	}
	// Re-query to get the full target info
	targets, err = cdpTargets()
	if err != nil {
		setCurrentTargetID(tabID)
		return &browserTarget{ID: tabID, URL: url}, nil
	}
	if target := findTargetByID(targets, tabID); target != nil {
		setCurrentTargetID(target.ID)
		return target, nil
	}
	setCurrentTargetID(tabID)
	return &browserTarget{ID: tabID, URL: url}, nil
}
