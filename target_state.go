package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Per-owner (tmux pane / pid) target selection state. Argus MCP processes are
// per-agent and restart freely (ppid watcher, client reconnects) — without
// persistence, every restart loses "which tab was I driving" and the agent
// hits "no target selected" mid-flow.
type targetState struct {
	TargetID string `json:"target_id,omitempty"`
	URL      string `json:"url,omitempty"`
	At       int64  `json:"at"`
}

func targetStatePath() string {
	return filepath.Join(homeDir(), ".config", "argus", "targets.json")
}

func loadTargetStates() map[string]targetState {
	m := map[string]targetState{}
	if data, err := os.ReadFile(targetStatePath()); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func loadTargetState() (targetState, bool) {
	st, ok := loadTargetStates()[sessionOwner()]
	return st, ok
}

// persistTargetState records the current selection for this owner.
// Atomic write (tmp+rename); last-writer-wins per pane is correct since
// each pane owns its own key.
func persistTargetState() {
	owner := sessionOwner()
	if owner == "" {
		return
	}
	m := loadTargetStates()
	url, _, _ := getConfig()
	m[owner] = targetState{
		TargetID: getCurrentTargetID(),
		URL:      url,
		At:       time.Now().Unix(),
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	p := targetStatePath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		_ = os.Rename(tmp, p)
	}
}
