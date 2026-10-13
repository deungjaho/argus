package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Causal-attribution ledger (argus#23). Every managed tab keeps a small ring
// buffer of input/navigation events tagged with the actor — an argus instance
// (via dispatch stamps), "user" (trusted events without a stamp), or "js"
// (untrusted, unstamped). Post-hoc attribution, not a gate: agents learn
// "someone else touched this" from receipts instead of misattributing a
// corrupted state to their own action.

var (
	argusInstanceOnce sync.Once
	argusInstance     string
)

// argusInstanceID identifies this argus process for ledger attribution.
func argusInstanceID() string {
	argusInstanceOnce.Do(func() { argusInstance = newInstanceID() })
	return argusInstance
}

// ledgerScript is appended to the per-tab injection bundle (picker.go).
const ledgerScript = `
(() => {
  if (window.__argusLedgerInit) return true;
  window.__argusLedgerInit = true;
  try { window.__argusLedger = JSON.parse(sessionStorage.getItem('__argusLedger') || '[]'); }
  catch(e) { window.__argusLedger = []; }
  const actorOf = (ev) => {
    const s = window.__argusStamp;
    if (s && Date.now() - s.ts < 500) return s.inst;
    return ev.isTrusted ? 'user' : 'js';
  };
  const brief = (el) => {
    if (!el || !el.tagName) return '';
    let d = el.tagName.toLowerCase();
    if (el.id) d += '#' + el.id;
    return d;
  };
  const push = (type, ev) => {
    window.__argusLedger.push({ ts: Date.now(), type, actor: actorOf(ev), detail: brief(ev.target) });
    if (window.__argusLedger.length > 50) window.__argusLedger.shift();
    try { sessionStorage.setItem('__argusLedger', JSON.stringify(window.__argusLedger)); } catch(e) {}
  };
  for (const t of ['click','keydown','input','wheel']) {
    document.addEventListener(t, e => push(t, e), { capture: true, passive: true });
  }
  let lastScroll = 0;
  document.addEventListener('scroll', e => {
    const now = Date.now();
    if (now - lastScroll > 400) { lastScroll = now; push('scroll', e); }
  }, { capture: true, passive: true });
  return true;
})()`

// ledgerStampJS returns a snippet that marks the following dispatch as ours.
func ledgerStampJS() string {
	return fmt.Sprintf(`window.__argusStamp={inst:%q,ts:Date.now()};`, argusInstanceID())
}

// ledgerStamp stamps the page before a trusted CDP dispatch so the injected
// listener attributes the resulting events to this argus instance.
func ledgerStamp(ws string) {
	_, _ = cdpEvaluate(ws, ledgerStampJS(), false)
}

type ledgerEvent struct {
	TS     int64  `json:"ts"`
	Type   string `json:"type"`
	Actor  string `json:"actor"`
	Detail string `json:"detail"`
}

func cdpLedgerRead(ws string) []ledgerEvent {
	v, err := cdpEvaluate(ws, `window.__argusLedger || []`, true)
	if err != nil {
		return nil
	}
	data, _ := json.Marshal(v)
	var evs []ledgerEvent
	_ = json.Unmarshal(data, &evs)
	return evs
}

// externalEvents returns ledger events not produced by this argus instance
// within the given window — the "someone touched this" signal.
func externalEvents(evs []ledgerEvent, windowMs int64) []ledgerEvent {
	me := argusInstanceID()
	cutoff := time.Now().UnixMilli() - windowMs
	out := []ledgerEvent{}
	for _, e := range evs {
		if e.TS >= cutoff && e.Actor != me && e.Actor != "" {
			out = append(out, e)
		}
	}
	return out
}

// attachExternalActivity mutates a tool result with an external_activity
// field when the ledger shows foreign input near this call. Reads stay
// silent — only writes surface it.
func attachExternalActivity(ws string, res map[string]any) {
	if res == nil {
		return
	}
	ext := externalEvents(cdpLedgerRead(ws), 30000)
	if len(ext) == 0 {
		return
	}
	last := ext[len(ext)-1]
	res["external_activity"] = map[string]any{
		"events":     len(ext),
		"last_type":  last.Type,
		"last_actor": last.Actor,
		"last_age_ms": time.Now().UnixMilli() - last.TS,
		"hint":       "someone else touched this tab — re-read state before trusting assumptions",
	}
}

// --- holder attribution for `tab list` ---

// tabHolders maps live tab IDs to their owning pane ("agent:%207") or "user"
// for tabs no agent registered. Reads the beacon registry file directly —
// a plain JSON read, not a shell-out, so it works even without beacon on PATH.
func tabHolders() map[string]map[string]any {
	out := map[string]map[string]any{}
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".local", "share", "beacon", "resources.json"))
	if err != nil {
		return out
	}
	var recs map[string]struct {
		Type      string         `json:"type"`
		ID        string         `json:"id"`
		OwnerPane string         `json:"owner_pane"`
		Metadata  map[string]any `json:"metadata"`
	}
	if json.Unmarshal(data, &recs) != nil {
		return out
	}
	for _, r := range recs {
		if r.Type == "tab" && r.ID != "" {
			m := map[string]any{"holder": "agent:" + r.OwnerPane}
			if tag, ok := r.Metadata["tag"].(string); ok && tag != "" {
				m["tag"] = tag
			}
			if sess, ok := r.Metadata["session"].(string); ok && sess != "" {
				m["session"] = sess
			}
			out[r.ID] = m
		}
	}
	return out
}

// holderOf returns the holder entry for a tab ID, defaulting to "user"
// for tabs no agent registered (manually opened in Chrome Argus).
func holderOf(holders map[string]map[string]any, tabID string) map[string]any {
	if h, ok := holders[tabID]; ok {
		return h
	}
	return map[string]any{"holder": "user"}
}
