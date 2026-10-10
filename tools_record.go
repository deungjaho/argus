package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- User-session recording → batch_actions replay ---
//
// start_recording injects a capture script into the page (and all future
// navigations via Page.addScriptToEvaluateOnNewDocument) that records
// trusted user input: clicks, field changes, key presses, navigations.
// stop_recording pulls the event log, converts it to batch_actions
// steps, saves it to disk, and returns the steps inline so the agent
// can replay the exact flow the human just demonstrated.

var (
	recordingMu     sync.Mutex
	recordingActive bool
	recordingWS     string
	recordingScript string // CDP script identifier from addScriptToEvaluateOnNewDocument
	recordingStart  time.Time
)

const recordingInjectJS = `(() => {
if (window.__argusRec) return true;
const events = [];
window.__argusRec = {events: events, started: Date.now()};
const push = (ev) => { if (events.length < 500) events.push(ev); };
push({type: 'navigate', url: location.href, t: Date.now()});
` + selectorForJS + `
document.addEventListener('click', (e) => {
  if (!e.isTrusted || !e.target || e.target.nodeType !== 1) return;
  push({type: 'click', selector: selectorFor(e.target), x: Math.round(e.clientX), y: Math.round(e.clientY), t: Date.now()});
}, true);
document.addEventListener('change', (e) => {
  const el = e.target;
  if (!el || !el.tagName) return;
  const tag = el.tagName;
  if (tag !== 'INPUT' && tag !== 'TEXTAREA' && tag !== 'SELECT') return;
  if (el.type === 'checkbox' || el.type === 'radio') return; // already captured by click
  push({type: 'fill', selector: selectorFor(el), value: String(el.value), t: Date.now()});
}, true);
document.addEventListener('keydown', (e) => {
  if (!e.isTrusted) return;
  if (e.key === 'Enter' || e.key === 'Escape' || e.key === 'Tab') {
    push({type: 'key', key: e.key, selector: selectorFor(e.target), t: Date.now()});
  }
}, true);
return true;
})()`

func registerRecordTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "start_recording",
		Description: "Start recording the human's interactions on the current tab (clicks, field changes, Enter/Tab/Escape, navigations). Recording survives page navigations. Call stop_recording to get replayable batch_actions steps.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input tabScopeInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("start_recording")
		defer rec.End()
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		recordingMu.Lock()
		defer recordingMu.Unlock()
		if recordingActive && recordingWS == ws {
			return nil, map[string]any{"recording": true, "already_active": true}, nil
		}
		// Inject now AND on every future navigation.
		if _, err := cdpEvaluate(ws, recordingInjectJS, true); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, fmt.Errorf("failed to install recorder: %w", err)
		}
		var scriptRes struct {
			Identifier string `json:"identifier"`
		}
		if err := cdpRequest(ws, "Page.addScriptToEvaluateOnNewDocument", map[string]any{
			"source": recordingInjectJS,
		}, &scriptRes); err != nil {
			rec.SetErrorFromError(fmt.Errorf("failed to persist recorder across navigations: %w", err))
			return nil, nil, fmt.Errorf("failed to persist recorder across navigations: %w", err)
		}
		recordingActive = true
		recordingWS = ws
		recordingScript = scriptRes.Identifier
		recordingStart = time.Now()
		result := map[string]any{"action_id": string(rec.ID), "recording": true, "started_at": recordingStart.Format(time.RFC3339)}
		rec.SetResult(result)
		return nil, result, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "stop_recording",
		Description: "Stop recording and return captured steps in batch_actions format (ready to replay). Also saves the recording to ~/.local/share/argus/recordings/.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input tabScopeInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("stop_recording")
		defer rec.End()
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		recordingMu.Lock()
		active, scriptID := recordingActive, recordingScript
		recordingActive = false
		recordingScript = ""
		recordingMu.Unlock()
		if !active {
			return nil, map[string]any{"recording": false, "steps": []any{}}, nil
		}
		if scriptID != "" {
			_ = cdpRequest(ws, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": scriptID}, nil)
		}
		value, err := cdpEvaluate(ws, `(() => {
const rec = window.__argusRec;
if (!rec) return {events: [], missing: true};
const out = {events: rec.events, started: rec.started};
window.__argusRec = null;
return out;
})()`, true)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		info, _ := value.(map[string]any)
		rawEvents, _ := info["events"].([]any)
		steps := eventsToBatchSteps(rawEvents)
		path, _ := saveRecording(steps)
		result := map[string]any{
			"action_id":     string(rec.ID),
			"recording":     false,
			"events":        len(rawEvents),
			"steps":         steps,
			"step_count":    len(steps),
			"replay_format": "pass steps as batch_actions.actions",
		}
		if path != "" {
			result["saved_to"] = path
		}
		rec.SetResult(result)
		return nil, result, nil
	})
}

// eventsToBatchSteps converts raw recorded events into batch_actions
// steps. Consecutive fill events on the same selector collapse to the
// last one (the final field value wins).
func eventsToBatchSteps(events []any) []map[string]any {
	steps := []map[string]any{}
	for _, raw := range events {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		sel, _ := m["selector"].(string)
		switch typ {
		case "navigate":
			url, _ := m["url"].(string)
			if url != "" && url != "about:blank" {
				steps = append(steps, map[string]any{"action": "navigate", "url": url})
			}
		case "click":
			if sel != "" {
				steps = append(steps, map[string]any{"action": "click", "selector": sel})
			}
		case "fill":
			val, _ := m["value"].(string)
			if sel == "" {
				continue
			}
			if n := len(steps); n > 0 {
				last := steps[n-1]
				if last["action"] == "fill" && last["selector"] == sel {
					last["value"] = val
					continue
				}
			}
			steps = append(steps, map[string]any{"action": "fill", "selector": sel, "value": val})
		case "key":
			key, _ := m["key"].(string)
			if key != "" {
				steps = append(steps, map[string]any{"action": "type", "key": key})
			}
		}
	}
	if len(steps) > 200 {
		steps = steps[:200]
	}
	return steps
}

func recordingsDir() string {
	if dir := strings.TrimSpace(os.Getenv("ARGUS_RECORDINGS_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "argus-recordings")
	}
	return filepath.Join(home, ".local", "share", "argus", "recordings")
}

// saveRecording writes steps JSON to the recordings dir. Never
// overwrites an existing file.
func saveRecording(steps []map[string]any) (string, error) {
	dir := recordingsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "rec-"+time.Now().Format("20060102-150405.000000000")+".json")
	data, err := json.MarshalIndent(map[string]any{"steps": steps}, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return path, nil
}
