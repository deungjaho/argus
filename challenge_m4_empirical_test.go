package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ============================================================================
// 1. TAB CONSOLIDATED TOOL EMPIRICAL CHALLENGES
// ============================================================================

func TestEmpirical_TabActionUnmarshalingAndCaseInsensitivity(t *testing.T) {
	testCases := []struct {
		name       string
		jsonInput  string
		wantAction string
		wantTabID  string
		wantURL    string
		wantBg     bool
	}{
		{
			name:       "uppercase NEW action with background flag",
			jsonInput:  `{"action": "NEW", "url": "https://example.com/app", "background": true}`,
			wantAction: "new",
			wantURL:    "https://example.com/app",
			wantBg:     true,
		},
		{
			name:       "whitespace-padded SELECT action with target_id alias",
			jsonInput:  `{"action": "  SELECT  ", "target_id": "TAB_90210"}`,
			wantAction: "select",
			wantTabID:  "TAB_90210",
		},
		{
			name:       "mixed-case cLoSe action with id alias",
			jsonInput:  `{"action": "cLoSe", "id": "TAB_DEAD"}`,
			wantAction: "close",
			wantTabID:  "TAB_DEAD",
		},
		{
			name:       "tab action with href alias",
			jsonInput:  `{"action": "new", "href": "https://github.com", "background": false}`,
			wantAction: "new",
			wantURL:    "https://github.com",
			wantBg:     false,
		},
		{
			name:       "tab action with title substring",
			jsonInput:  `{"action": "select", "title": "Admin Dashboard"}`,
			wantAction: "select",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var in tabInput
			if err := json.Unmarshal([]byte(tc.jsonInput), &in); err != nil {
				t.Fatalf("failed to unmarshal tabInput: %v", err)
			}
			normAction := strings.ToLower(strings.TrimSpace(in.Action))
			if normAction != tc.wantAction {
				t.Errorf("action mismatch: got %q, want %q", normAction, tc.wantAction)
			}
			tabID := resolveFirstNonEmpty(in.TabID, in.TargetID, in.ID)
			if tabID != tc.wantTabID {
				t.Errorf("tabID mismatch: got %q, want %q", tabID, tc.wantTabID)
			}
			url := resolveURL(in.URL, in.Href, in.Target)
			if url != tc.wantURL {
				t.Errorf("url mismatch: got %q, want %q", url, tc.wantURL)
			}
			if in.Background != tc.wantBg {
				t.Errorf("background mismatch: got %v, want %v", in.Background, tc.wantBg)
			}
		})
	}
}

func TestEmpirical_TabActionInferenceAndPrecedence(t *testing.T) {
	inferAction := func(in tabInput) string {
		action := strings.ToLower(strings.TrimSpace(in.Action))
		if action == "" {
			if in.TabID != "" || in.TargetID != "" || in.ID != "" || in.Title != "" {
				action = "select"
			} else if in.URL != "" || in.Href != "" {
				action = "new"
			} else {
				action = "list"
			}
		}
		return action
	}

	// 1. Both TabID and URL specified without action -> TabID takes precedence -> select
	conflicted := tabInput{
		TabID: "TAB_123",
		URL:   "https://conflict.com",
	}
	if act := inferAction(conflicted); act != "select" {
		t.Errorf("expected TabID to take precedence for select inference, got %q", act)
	}

	// 2. Both TargetID and Href specified -> select
	conflicted2 := tabInput{
		TargetID: "TAB_456",
		Href:     "https://conflict.com",
	}
	if act := inferAction(conflicted2); act != "select" {
		t.Errorf("expected TargetID to take precedence for select inference, got %q", act)
	}

	// 3. Only Href specified -> new
	hrefOnly := tabInput{
		Href: "https://example.org",
	}
	if act := inferAction(hrefOnly); act != "new" {
		t.Errorf("expected Href to infer 'new', got %q", act)
	}

	// 4. Whitespace-only action with title -> select
	titleWithWhitespaceAction := tabInput{
		Action: "   ",
		Title:  "Argus Settings",
	}
	if act := inferAction(titleWithWhitespaceAction); act != "select" {
		t.Errorf("expected select for title with whitespace action, got %q", act)
	}

	// 5. Completely empty struct -> list
	if act := inferAction(tabInput{}); act != "list" {
		t.Errorf("expected empty input to default to list, got %q", act)
	}
}

func TestEmpirical_TabTargetDisambiguation(t *testing.T) {
	// When target looks like a URL vs when target looks like a tab ID
	urlTarget := "https://internal.dashboard/view"
	if !looksLikeURL(urlTarget) {
		t.Errorf("expected %q to look like a URL", urlTarget)
	}

	idTarget := "CDP_PAGE_883719"
	if looksLikeURL(idTarget) {
		t.Errorf("expected %q to NOT look like a URL", idTarget)
	}

	// localhost and port
	localTarget := "localhost:3000/app"
	if !looksLikeURL(localTarget) {
		t.Errorf("expected %q to look like a URL", localTarget)
	}
}

// ============================================================================
// 2. STORAGE CONSOLIDATED TOOL EMPIRICAL CHALLENGES
// ============================================================================

func TestEmpirical_StorageActionAndTypeResolution(t *testing.T) {
	typeCases := []struct {
		rawType  string
		expected string
	}{
		{"COOKIE", "cookie"},
		{"cookies", "cookie"},
		{"  Cookie  ", "cookie"},
		{"local", "local"},
		{"LOCALSTORAGE", "local"},
		{"local_storage", "local"},
		{"Session", "session"},
		{"sessionStorage", "session"},
		{"session_storage", "session"},
		{"ALL", "all"},
		{"*", "all"},
		{"", ""},
	}

	for _, tc := range typeCases {
		res := resolveStorageType(tc.rawType)
		if res != tc.expected {
			t.Errorf("resolveStorageType(%q) = %q, want %q", tc.rawType, res, tc.expected)
		}
	}
}

func TestEmpirical_StorageActionDefaulting(t *testing.T) {
	resolveAction := func(in storageInput) string {
		action := strings.ToLower(strings.TrimSpace(in.Action))
		if action == "" {
			if in.Value != "" {
				action = "set"
			} else {
				action = "get"
			}
		}
		return action
	}

	// Value present -> set
	if act := resolveAction(storageInput{Value: "some_token"}); act != "set" {
		t.Errorf("expected inferred 'set' when value present, got %q", act)
	}

	// Value empty -> get
	if act := resolveAction(storageInput{Key: "session"}); act != "get" {
		t.Errorf("expected inferred 'get' when value empty, got %q", act)
	}

	// Explicit action overrides value presence
	if act := resolveAction(storageInput{Action: "clear", Value: "ignored"}); act != "clear" {
		t.Errorf("expected explicit action 'clear', got %q", act)
	}
}

func TestEmpirical_StorageJSONKeyAndValueEdgeCases(t *testing.T) {
	complexValues := []string{
		`{"user":"admin","roles":["root","dev"],"id":12345}`,
		"Line 1\nLine 2\r\nLine 3\tTabbed",
		"Quotes \"single 'and' double\" and backslash \\",
		"Unicode: 中文测试 🚀 🔥 ãéíóú 🍺",
		"",
	}

	for _, val := range complexValues {
		rawJSON := fmt.Sprintf(`{"action": "set", "type": "localStorage", "key": "test_key", "value": %s}`, jsString(val))
		var in storageInput
		if err := json.Unmarshal([]byte(rawJSON), &in); err != nil {
			t.Fatalf("failed to unmarshal JSON with value %q: %v", val, err)
		}
		if in.Value != val {
			t.Errorf("value unmarshaling mismatch: got %q, want %q", in.Value, val)
		}

		// Ensure jsString escaping roundtrips cleanly without JS syntax errors
		escaped := jsString(in.Value)
		if !strings.HasPrefix(escaped, `"`) || !strings.HasSuffix(escaped, `"`) {
			t.Errorf("expected jsString to produce quoted string, got: %s", escaped)
		}
	}
}

func TestEmpirical_StorageParameterAliases(t *testing.T) {
	rawJSON := `{
		"storage_type": "session_storage",
		"name": "auth_header",
		"target": "session"
	}`
	var in storageInput
	if err := json.Unmarshal([]byte(rawJSON), &in); err != nil {
		t.Fatalf("failed to unmarshal storage aliases: %v", err)
	}

	st := resolveStorageType(resolveFirstNonEmpty(in.Type, in.StorageType, in.Target))
	if st != "session" {
		t.Errorf("expected resolved storage type 'session', got %q", st)
	}

	key := resolveFirstNonEmpty(in.Key, in.Name)
	if key != "auth_header" {
		t.Errorf("expected resolved key 'auth_header', got %q", key)
	}
}

// ============================================================================
// 3. GET_ELEMENT CONSOLIDATED TOOL EMPIRICAL CHALLENGES
// ============================================================================

func TestEmpirical_GetElementCoordinatesExtraction(t *testing.T) {
	// Case 1: Pure X/Y coordinates
	rawJSON1 := `{
		"x": 345.75,
		"y": 789.5
	}`
	var in1 getElementInput
	if err := json.Unmarshal([]byte(rawJSON1), &in1); err != nil {
		t.Fatalf("failed to unmarshal coordinates: %v", err)
	}

	if in1.X == nil || *in1.X != 345.75 {
		t.Fatalf("expected X=345.75, got %v", in1.X)
	}
	if in1.Y == nil || *in1.Y != 789.5 {
		t.Fatalf("expected Y=789.5, got %v", in1.Y)
	}

	x1, y1, err := resolveInspectPoint("ws://mock", in1)
	if err != nil {
		t.Fatalf("resolveInspectPoint failed: %v", err)
	}
	if x1 != 345.75 || y1 != 789.5 {
		t.Errorf("resolveInspectPoint coordinates mismatch: got (%f, %f), want (345.75, 789.5)", x1, y1)
	}

	// Case 2: ImageX/ImageY coordinates
	rawJSON2 := `{
		"image_x": 100.0,
		"image_y": 200.0
	}`
	var in2 getElementInput
	if err := json.Unmarshal([]byte(rawJSON2), &in2); err != nil {
		t.Fatalf("failed to unmarshal image coordinates: %v", err)
	}

	x2, y2, err := resolveInspectPoint("ws://mock", in2)
	if err != nil {
		t.Fatalf("resolveInspectPoint failed: %v", err)
	}
	if x2 != 100.0 || y2 != 200.0 {
		t.Errorf("resolveInspectPoint image coordinates mismatch: got (%f, %f), want (100.0, 200.0)", x2, y2)
	}
}

func TestEmpirical_GetElementSelectorPrecedenceAndTrimming(t *testing.T) {
	// 1. Ref has precedence over Selector, CSS, Target, XPath
	rawJSON := `{
		"ref": "ax:99",
		"selector": "#submit-btn",
		"css": ".primary-btn",
		"target": "input[type='submit']",
		"xpath": "//button"
	}`
	var in getElementInput
	if err := json.Unmarshal([]byte(rawJSON), &in); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	sel := resolveSelector(in.Ref, in.Selector, in.CSS, in.Target, in.XPath, "")
	if sel != "ax:99" {
		t.Errorf("expected Ref to take precedence, got %q", sel)
	}

	// 2. When Ref is empty, Selector takes precedence over CSS
	sel2 := resolveSelector("", in.Selector, in.CSS, in.Target, in.XPath, "")
	if sel2 != "#submit-btn" {
		t.Errorf("expected Selector to take precedence over CSS, got %q", sel2)
	}

	// 3. Trimming whitespace around selector
	selPadded := resolveSelector("  ", "   #trimmed-selector   ", "", "", "", "")
	if selPadded != "#trimmed-selector" {
		t.Errorf("expected trimmed selector, got %q", selPadded)
	}

	// 4. Query string trimming and fallback to Text
	inQuery := getElementInput{
		Query: "   Search Query Text   ",
	}
	searchText := resolveFirstNonEmpty(inQuery.Text, inQuery.Query, inQuery.Pattern)
	if searchText != "Search Query Text" {
		t.Errorf("expected trimmed query search text, got %q", searchText)
	}
}

func TestEmpirical_GetElementA11yAndBackendNodeIDFormatting(t *testing.T) {
	formatSelector := func(in getElementInput) string {
		sel := resolveSelector(in.Ref, in.Selector, in.CSS, in.Target, in.XPath, "")
		if sel == "" {
			if in.NodeID != "" {
				sel = "ax:" + in.NodeID
			} else if in.BackendNodeID > 0 {
				sel = fmt.Sprintf("backend:%d", in.BackendNodeID)
			} else if in.BackendDOMNodeID > 0 {
				sel = fmt.Sprintf("backend:%d", in.BackendDOMNodeID)
			}
		}
		return sel
	}

	// NodeID
	if s := formatSelector(getElementInput{NodeID: "101"}); s != "ax:101" {
		t.Errorf("expected ax:101, got %q", s)
	}

	// BackendNodeID
	if s := formatSelector(getElementInput{BackendNodeID: 554}); s != "backend:554" {
		t.Errorf("expected backend:554, got %q", s)
	}

	// BackendDOMNodeID
	if s := formatSelector(getElementInput{BackendDOMNodeID: 772}); s != "backend:772" {
		t.Errorf("expected backend:772, got %q", s)
	}
}

// ============================================================================
// 4. WAIT_FOR CONSOLIDATED TOOL EMPIRICAL CHALLENGES
// ============================================================================

func TestEmpirical_WaitForTimeoutZeroVsOmitted(t *testing.T) {
	// Case A: Explicit timeout_ms = 0
	var inZero waitForInput
	if err := json.Unmarshal([]byte(`{"selector": "#submit", "timeout_ms": 0}`), &inZero); err != nil {
		t.Fatalf("unmarshal zero failed: %v", err)
	}
	if !inZero.TimeoutSpecified {
		t.Errorf("expected TimeoutSpecified=true for explicit timeout_ms=0")
	}
	if inZero.TimeoutMs != 0 {
		t.Errorf("expected TimeoutMs=0, got %d", inZero.TimeoutMs)
	}

	// Case B: Explicit timeout = 0 (alias)
	var inZeroAlias waitForInput
	if err := json.Unmarshal([]byte(`{"selector": "#submit", "timeout": 0}`), &inZeroAlias); err != nil {
		t.Fatalf("unmarshal zero alias failed: %v", err)
	}
	if !inZeroAlias.TimeoutSpecified || inZeroAlias.TimeoutMs != 0 {
		t.Errorf("expected TimeoutSpecified=true and TimeoutMs=0 for timeout: 0")
	}

	// Case C: Omitted timeout
	var inOmitted waitForInput
	if err := json.Unmarshal([]byte(`{"selector": "#submit"}`), &inOmitted); err != nil {
		t.Fatalf("unmarshal omitted failed: %v", err)
	}
	if inOmitted.TimeoutSpecified {
		t.Errorf("expected TimeoutSpecified=false when timeout omitted")
	}
}

func TestEmpirical_WaitForPositiveTimeoutBug(t *testing.T) {
	raw := `{"selector": "#submit", "timeout_ms": 2500}`
	var in waitForInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// This is the EXACT check from tools_wait.go:87:
	isInstantExists := in.TimeoutSpecified && (in.TimeoutMs == 0 || in.Timeout == 0)

	t.Logf("in.TimeoutSpecified = %v", in.TimeoutSpecified)
	t.Logf("in.TimeoutMs = %d", in.TimeoutMs)
	t.Logf("in.Timeout = %d", in.Timeout)
	t.Logf("isInstantExists = %v", isInstantExists)

	// An explicit timeout of 2500ms MUST NOT be treated as an instant exists check (timeout=0).
	if isInstantExists {
		t.Errorf("CRITICAL BUG FOUND in tools_wait.go:87: wait_for falsely evaluates isInstantExists=true when timeout_ms=2500 because in.Timeout is uninitialized (0), turning wait_for into an instant check that never waits!")
	}
}

func TestEmpirical_WaitForExpressionGenerationEdgeCases(t *testing.T) {
	// Combination of selector, text, text_absent, and url_matches
	input := waitForInput{
		Selector:   ".toast-success",
		Text:       "Changes saved",
		TextAbsent: "Saving...",
		URLMatches: "/dashboard",
	}

	expr := waitForExpression(input)
	if !strings.Contains(expr, ".toast-success") {
		t.Errorf("missing selector check in generated expression: %s", expr)
	}
	if !strings.Contains(expr, "Changes saved") {
		t.Errorf("missing text present check in generated expression: %s", expr)
	}
	if !strings.Contains(expr, "Saving...") {
		t.Errorf("missing text absent check in generated expression: %s", expr)
	}
	if !strings.Contains(expr, "/dashboard") {
		t.Errorf("missing url matches check in generated expression: %s", expr)
	}
	if !strings.Contains(expr, "Object.values(checks).every") {
		t.Errorf("missing satisfaction aggregation logic in expression: %s", expr)
	}
}

func TestEmpirical_WaitForDelayFallbacks(t *testing.T) {
	// 1. time_ms takes precedence
	in1 := waitForInput{TimeMs: 1500, SleepMs: 2000, DurationMs: 2500}
	sleepMs1 := in1.TimeMs
	if sleepMs1 != 1500 {
		t.Errorf("expected time_ms=1500, got %d", sleepMs1)
	}

	// 2. sleep_ms takes second precedence
	in2 := waitForInput{SleepMs: 2000, DurationMs: 2500}
	sleepMs2 := in2.TimeMs
	if sleepMs2 <= 0 {
		sleepMs2 = in2.SleepMs
	}
	if sleepMs2 != 2000 {
		t.Errorf("expected sleep_ms=2000, got %d", sleepMs2)
	}

	// 3. delay conversion: delay <= 60 is treated as seconds -> * 1000
	converted := resolveTimeout(0, 5)
	if converted != 5000 {
		t.Errorf("expected delay 5s to convert to 5000ms, got %d", converted)
	}

	// delay > 60 is treated as ms directly
	convertedMs := resolveTimeout(0, 500)
	if convertedMs != 500 {
		t.Errorf("expected delay 500 to remain 500ms, got %d", convertedMs)
	}
}

// ============================================================================
// 5. SCREENSHOT CONSOLIDATED TOOL EMPIRICAL CHALLENGES
// ============================================================================

func TestEmpirical_ScreenshotFullPageParameterResolution(t *testing.T) {
	// full_page snake_case
	var inSnake screenshotInput
	if err := json.Unmarshal([]byte(`{"full_page": true}`), &inSnake); err != nil {
		t.Fatalf("unmarshal full_page failed: %v", err)
	}
	if !inSnake.FullPage {
		t.Errorf("expected full_page=true")
	}

	// fullPage camelCase
	var inCamel screenshotInput
	if err := json.Unmarshal([]byte(`{"fullPage": true}`), &inCamel); err != nil {
		t.Fatalf("unmarshal fullPage failed: %v", err)
	}
	if !inCamel.FullPageCamel {
		t.Errorf("expected fullPage=true")
	}

	// Combined resolution check
	isFullPage := inCamel.FullPage || inCamel.FullPageCamel
	if !isFullPage {
		t.Errorf("expected combined full page resolution to be true")
	}
}

func TestEmpirical_ScreenshotPathResolutionAndAliases(t *testing.T) {
	// 1. Path alias resolution precedence
	p1 := resolveInputPath("  ", "/custom/path.png", "/another/path.png")
	if p1 != "/custom/path.png" {
		t.Errorf("expected first non-empty path, got %q", p1)
	}

	// 2. Empty path defaults to temp directory with timestamped jpg
	pEmpty, err := resolveScreenshotPath("", "")
	if err != nil {
		t.Fatalf("resolveScreenshotPath empty failed: %v", err)
	}
	if !strings.HasPrefix(pEmpty, os.TempDir()) {
		t.Errorf("expected path in temp dir, got %q", pEmpty)
	}
	if !strings.HasSuffix(pEmpty, ".jpg") {
		t.Errorf("expected .jpg extension for default empty format, got %q", pEmpty)
	}

	// 3. Empty path with png format
	pEmptyPNG, err := resolveScreenshotPath("", "png")
	if err != nil {
		t.Fatalf("resolveScreenshotPath empty png failed: %v", err)
	}
	if !strings.HasSuffix(pEmptyPNG, ".png") {
		t.Errorf("expected .png extension, got %q", pEmptyPNG)
	}

	// 4. Directory with trailing slash
	tmpDir, err := os.MkdirTemp("", "argus-challenge-m4-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pDir, err := resolveScreenshotPath(tmpDir+string(filepath.Separator), "jpeg")
	if err != nil {
		t.Fatalf("resolveScreenshotPath dir failed: %v", err)
	}
	if !strings.HasPrefix(pDir, tmpDir) {
		t.Errorf("expected path to reside inside %q, got %q", tmpDir, pDir)
	}
	if !strings.HasSuffix(pDir, ".jpg") {
		t.Errorf("expected .jpg extension, got %q", pDir)
	}

	// 5. User home directory expansion
	pHome, err := resolveScreenshotPath("~/argus-test-shot.png", "")
	if err != nil {
		t.Fatalf("resolveScreenshotPath home failed: %v", err)
	}
	if strings.HasPrefix(pHome, "~") {
		t.Errorf("expected '~' to be expanded, got %q", pHome)
	}
	if !strings.HasSuffix(pHome, "argus-test-shot.png") {
		t.Errorf("expected filename preserved, got %q", pHome)
	}
}

func TestEmpirical_ScreenshotDiskModePayloadContract(t *testing.T) {
	// Test save_to_disk and disk aliases
	var inDisk screenshotInput
	if err := json.Unmarshal([]byte(`{"disk": true, "path": "/tmp/out.jpg"}`), &inDisk); err != nil {
		t.Fatalf("unmarshal disk failed: %v", err)
	}
	if !inDisk.Disk {
		t.Errorf("expected disk=true")
	}
	saveToDisk := inDisk.SaveToDisk || inDisk.Disk
	if !saveToDisk {
		t.Errorf("expected saveToDisk=true")
	}

	var inSaveToDisk screenshotInput
	if err := json.Unmarshal([]byte(`{"save_to_disk": true}`), &inSaveToDisk); err != nil {
		t.Fatalf("unmarshal save_to_disk failed: %v", err)
	}
	if !inSaveToDisk.SaveToDisk {
		t.Errorf("expected save_to_disk=true")
	}
	if !(inSaveToDisk.SaveToDisk || inSaveToDisk.Disk) {
		t.Errorf("expected combined saveToDisk=true")
	}
}

// ============================================================================
// 6. TOOL REGISTRATION INTEGRITY VERIFICATION FOR ALL 5 TOOLS
// ============================================================================

func TestEmpirical_All5ConsolidatedToolsRegistration(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-m4-empirical", Version: "1.0"}, nil)
	registerTabTools(server)
	registerStorageTools(server)
	registerElementTools(server)
	registerWaitTools(server)
	registerScreenshotTools(server)

	// Verify tool registration succeeds cleanly without panicking
}
