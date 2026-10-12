package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// DialogPolicy defines the automatic handling behavior when a JavaScript dialog opens.
type DialogPolicy string

const (
	DialogPolicyDismiss DialogPolicy = "dismiss" // Auto-dismiss (default for headless automation)
	DialogPolicyAccept  DialogPolicy = "accept"  // Auto-accept with default/configured text
	DialogPolicyNone    DialogPolicy = "none"    // Keep pending; do not auto-handle
)

// DialogInfo represents the state and metadata of a JavaScript dialog.
type DialogInfo struct {
	Type              string    `json:"type"`                          // "alert", "confirm", "prompt", "beforeunload"
	Message           string    `json:"message"`                       // message text in dialog
	URL               string    `json:"url,omitempty"`                 // frame URL where dialog appeared
	DefaultPrompt     string    `json:"default_prompt,omitempty"`      // default prompt text for prompt dialogs
	HasBrowserHandler bool      `json:"has_browser_handler,omitempty"` // true if handled by browser
	OpenedAt          time.Time `json:"opened_at"`                     // timestamp when opened
	Handled           bool      `json:"handled"`                       // whether dialog was handled
	HandledAction     string    `json:"handled_action,omitempty"`      // "accept" or "dismiss"
	HandledPromptText string    `json:"handled_prompt_text,omitempty"` // text supplied if accepted
	HandledAt         time.Time `json:"handled_at,omitempty"`          // timestamp when handled
	AutoHandled       bool      `json:"auto_handled"`                  // whether handled automatically by policy
}

// TargetDialogState tracks dialog state for a specific target tab.
type TargetDialogState struct {
	mu          sync.RWMutex
	targetID    string
	wsURL       string
	policy      DialogPolicy
	promptText  string
	pending     *DialogInfo
	lastHandled *DialogInfo
	history     []DialogInfo
	notifyCh    chan struct{}
}

func newTargetDialogState(targetID, wsURL string, policy DialogPolicy) *TargetDialogState {
	return &TargetDialogState{
		targetID: targetID,
		wsURL:    wsURL,
		policy:   policy,
		history:  make([]DialogInfo, 0, 10),
		notifyCh: make(chan struct{}, 1),
	}
}

func (s *TargetDialogState) Pending() *DialogInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pending == nil {
		return nil
	}
	clone := *s.pending
	return &clone
}

func (s *TargetDialogState) HasPending() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pending != nil
}

func (s *TargetDialogState) SetPending(info *DialogInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = info
	s.notifyLocked()
}

func (s *TargetDialogState) LastHandled() *DialogInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastHandled == nil {
		return nil
	}
	clone := *s.lastHandled
	return &clone
}

// Last returns the most recent dialog info (pending, last handled, or from history).
func (s *TargetDialogState) Last() *DialogInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pending != nil {
		clone := *s.pending
		return &clone
	}
	if s.lastHandled != nil {
		clone := *s.lastHandled
		return &clone
	}
	if len(s.history) > 0 {
		clone := s.history[len(s.history)-1]
		return &clone
	}
	return nil
}

func (s *TargetDialogState) History() []DialogInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]DialogInfo, len(s.history))
	copy(res, s.history)
	return res
}

func (s *TargetDialogState) GetPolicy() (DialogPolicy, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policy, s.promptText
}

func (s *TargetDialogState) SetPolicy(policy DialogPolicy, promptText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = policy
	s.promptText = promptText
}

func (s *TargetDialogState) MarkHandled(accepted bool, promptText string, auto bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		s.pending.Handled = true
		s.pending.HandledAt = time.Now()
		if accepted {
			s.pending.HandledAction = "accept"
		} else {
			s.pending.HandledAction = "dismiss"
		}
		s.pending.HandledPromptText = promptText
		s.pending.AutoHandled = auto
		s.lastHandled = s.pending
		s.addToHistoryLocked(*s.pending)
		s.pending = nil
	}
	s.drainNotifyLocked()
}

func (s *TargetDialogState) MarkClosed(result bool, userInput string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		s.pending.Handled = true
		s.pending.HandledAt = time.Now()
		if result {
			s.pending.HandledAction = "accept"
		} else {
			s.pending.HandledAction = "dismiss"
		}
		s.pending.HandledPromptText = userInput
		s.lastHandled = s.pending
		s.addToHistoryLocked(*s.pending)
		s.pending = nil
	}
	s.drainNotifyLocked()
}

func (s *TargetDialogState) addToHistoryLocked(item DialogInfo) {
	if len(s.history) >= 10 {
		s.history = s.history[1:]
	}
	s.history = append(s.history, item)
}

func (s *TargetDialogState) notifyLocked() {
	select {
	case s.notifyCh <- struct{}{}:
	default:
	}
}

func (s *TargetDialogState) drainNotifyLocked() {
	select {
	case <-s.notifyCh:
	default:
	}
}

// DrainNotify non-blockingly drains any pending notification token from notifyCh.
func (s *TargetDialogState) DrainNotify() {
	select {
	case <-s.notifyCh:
	default:
	}
}

// DialogManager manages dialog states across all browser targets.
type DialogManager struct {
	mu            sync.RWMutex
	targets       map[string]*TargetDialogState
	defaultPolicy DialogPolicy
}

var dialogMgr = newDialogManager()

func newDialogManager() *DialogManager {
	return &DialogManager{
		targets:       make(map[string]*TargetDialogState),
		defaultPolicy: defaultDialogPolicyFromEnv(),
	}
}

func defaultDialogPolicyFromEnv() DialogPolicy {
	val := strings.ToLower(strings.TrimSpace(os.Getenv("ARGUS_DIALOG_POLICY")))
	switch val {
	case "accept", "auto_accept":
		return DialogPolicyAccept
	case "manual", "none":
		return DialogPolicyNone
	case "dismiss", "auto_dismiss":
		return DialogPolicyDismiss
	default:
		return DialogPolicyDismiss
	}
}

func (m *DialogManager) GetOrCreate(targetID, wsURL string) *TargetDialogState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.targets[targetID]; ok {
		if wsURL != "" && s.wsURL == "" {
			s.wsURL = wsURL
		}
		return s
	}
	s := newTargetDialogState(targetID, wsURL, m.defaultPolicy)
	m.targets[targetID] = s
	return s
}

func (m *DialogManager) Get(targetID string) *TargetDialogState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.targets[targetID]; ok {
		return s
	}
	trimmed := strings.TrimSpace(targetID)
	if trimmed == "" {
		return nil
	}
	for id, s := range m.targets {
		if id == trimmed || shortID(id) == trimmed || (len(trimmed) >= 4 && strings.HasPrefix(id, trimmed)) {
			return s
		}
	}
	return nil
}

func (m *DialogManager) HasPendingDialog(targetID string) bool {
	s := m.Get(targetID)
	if s == nil {
		return false
	}
	return s.HasPending()
}

func (m *DialogManager) GetPendingDialog(targetID string) *DialogInfo {
	s := m.Get(targetID)
	if s == nil {
		return nil
	}
	return s.Pending()
}

func (m *DialogManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = make(map[string]*TargetDialogState)
	m.defaultPolicy = defaultDialogPolicyFromEnv()
}

// Remove drops a target's dialog state when the tab is closed.
func (m *DialogManager) Remove(targetID string) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.targets, targetID)
	for id := range m.targets {
		if id == targetID || shortID(id) == targetID || (len(targetID) >= 4 && strings.HasPrefix(id, targetID)) {
			delete(m.targets, id)
		}
	}
}

// startDialogSubscriber initializes the real-time background listener on conn.
func startDialogSubscriber(conn *cdpConn, wsURL string) {
	if conn == nil || !isPageEndpoint(wsURL) {
		return
	}
	targetID := targetIDFromWSURL(wsURL)
	dialogMgr.GetOrCreate(targetID, wsURL)

	sub := conn.subscribeEvents("dialog", 32, false)
	go runDialogSubscriber(sub, conn, wsURL, targetID)
}

func runDialogSubscriber(sub *eventSubscription, conn *cdpConn, wsURL, targetID string) {
	defer func() {
		if r := recover(); r != nil {
			logWarn("dialog subscriber panic: %v", r)
		}
	}()

	for envelope := range sub.ch {
		if envelope == nil {
			continue
		}
		switch envelope.Method {
		case "Page.javascriptDialogOpening":
			handleCDPDialogOpening(envelope, conn, wsURL, targetID)
		case "Page.javascriptDialogClosed":
			handleCDPDialogClosed(envelope, targetID)
		}
	}
}

func handleCDPDialogOpening(envelope *browserCDPEnvelope, conn *cdpConn, wsURL, targetID string) {
	var params struct {
		URL               string `json:"url"`
		Message           string `json:"message"`
		Type              string `json:"type"`
		HasBrowserHandler bool   `json:"hasBrowserHandler"`
		DefaultPrompt     string `json:"defaultPrompt"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		logWarn("dialog: failed to unmarshal opening params: %v", err)
		return
	}

	info := &DialogInfo{
		Type:              params.Type,
		Message:           params.Message,
		URL:               params.URL,
		DefaultPrompt:     params.DefaultPrompt,
		HasBrowserHandler: params.HasBrowserHandler,
		OpenedAt:          time.Now(),
	}

	state := dialogMgr.GetOrCreate(targetID, wsURL)
	state.SetPending(info)

	var epoch ConnectionEpoch
	if conn != nil {
		epoch = conn.epoch
	}
	rec.RecordEventWithAction("dialog", targetID, "", epoch, map[string]any{
		"type":           params.Type,
		"message":        params.Message,
		"url":            params.URL,
		"default_prompt": params.DefaultPrompt,
		"status":         "opening",
	})

	policy, promptText := state.GetPolicy()
	switch policy {
	case DialogPolicyDismiss:
		go func() {
			if conn != nil && conn.ws != nil && conn.alive() {
				_ = conn.call("Page.handleJavaScriptDialog", map[string]any{"accept": false}, nil, 2*time.Second)
			}
			state.MarkHandled(false, "", true)
			rec.RecordEventWithAction("dialog", targetID, "", epoch, map[string]any{
				"type":    params.Type,
				"message": params.Message,
				"status":  "auto_dismissed",
			})
		}()
	case DialogPolicyAccept:
		go func() {
			effectivePrompt := promptText
			if effectivePrompt == "" && params.DefaultPrompt != "" {
				effectivePrompt = params.DefaultPrompt
			}
			handleParams := map[string]any{"accept": true}
			if params.Type == "prompt" && effectivePrompt != "" {
				handleParams["promptText"] = effectivePrompt
			}
			if conn != nil && conn.ws != nil && conn.alive() {
				_ = conn.call("Page.handleJavaScriptDialog", handleParams, nil, 2*time.Second)
			}
			state.MarkHandled(true, effectivePrompt, true)
			rec.RecordEventWithAction("dialog", targetID, "", epoch, map[string]any{
				"type":        params.Type,
				"message":     params.Message,
				"prompt_text": effectivePrompt,
				"status":      "auto_accepted",
			})
		}()
	}
}

func handleCDPDialogClosed(envelope *browserCDPEnvelope, targetID string) {
	var params struct {
		Result    bool   `json:"result"`
		UserInput string `json:"userInput"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	dlgType := ""
	msg := ""
	state := dialogMgr.Get(targetID)
	if state != nil {
		state.MarkClosed(params.Result, params.UserInput)
		if last := state.Last(); last != nil {
			dlgType = last.Type
			msg = last.Message
		}
	}

	// Fallback to recent recorder events if state did not provide metadata
	if dlgType == "" || msg == "" {
		events := rec.QueryEvents(EventFilter{targetID: targetID, limit: 10})
		for _, e := range events {
			if e.Type == "dialog" {
				if m, ok := e.Data.(map[string]any); ok {
					if dlgType == "" {
						if t, ok := m["type"].(string); ok && t != "" {
							dlgType = t
						}
					}
					if msg == "" {
						if mStr, ok := m["message"].(string); ok && mStr != "" {
							msg = mStr
						}
					}
					if dlgType != "" && msg != "" {
						break
					}
				}
			}
		}
	}

	rec.RecordEventWithAction("dialog", targetID, "", "", map[string]any{
		"status":     "closed",
		"type":       dlgType,
		"message":    msg,
		"accepted":   params.Result,
		"user_input": params.UserInput,
	})
}
