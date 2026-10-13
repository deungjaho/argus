package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Beacon producer integration (argus#11): report tab lifecycle keyed by
// TMUX_PANE to the beacon registry. All calls are best-effort shell-outs —
// when beacon is absent argus stays fully standalone.

var (
	beaconPathOnce sync.Once
	beaconPath     string
	sessionPaneVal = os.Getenv("TMUX_PANE")
)

func beaconBin() string {
	beaconPathOnce.Do(func() {
		beaconPath, _ = exec.LookPath("beacon")
	})
	return beaconPath
}

func beaconEnabled() bool {
	return sessionPaneVal != "" && beaconBin() != ""
}

func beaconCall(args ...string) {
	if !beaconEnabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, beaconBin(), args...).Run()
}

// beaconRegisterTab records a tab owned by this pane.
func beaconRegisterTab(tabID, url string) {
	if tabID == "" {
		return
	}
	meta, _ := json.Marshal(map[string]string{"url": url})
	beaconCall("register", "tab", tabID, "--pane", sessionPaneVal, "--producer", "argus", "--data", string(meta))
}

func beaconUnregisterTab(tabID string) {
	if tabID == "" {
		return
	}
	beaconCall("unregister", "tab", tabID)
}

// beaconStaleTabs returns registry tab records whose owner pane is dead.
func beaconStaleTabs() []string {
	if !beaconEnabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, beaconBin(), "resources", "--type", "tab", "--stale", "--json").Output()
	if err != nil {
		return nil
	}
	var recs []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &recs) != nil {
		return nil
	}
	var ids []string
	for _, r := range recs {
		ids = append(ids, r.ID)
	}
	return ids
}

var tabGCLast time.Time

// maybeRunTabGC reaps Chrome tabs owned by dead panes, at most once per
// 30s per process. Runs inline on tool calls — cheap when idle.
func maybeRunTabGC() {
	if time.Since(tabGCLast) < 30*time.Second {
		return
	}
	tabGCLast = time.Now()
	for _, tabID := range beaconStaleTabs() {
		if err := cdpCloseTarget(tabID); err == nil {
			beaconUnregisterTab(tabID)
		}
	}
}
