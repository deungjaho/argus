package main

import (
	"encoding/json"
	"testing"
)

func TestResolveFirstNonEmpty(t *testing.T) {
	tests := []struct {
		inputs []string
		want   string
	}{
		{[]string{"", "  ", "hello", "world"}, "hello"},
		{[]string{"", ""}, ""},
		{[]string{"first", "second"}, "first"},
		{[]string{"  spaced  "}, "spaced"},
	}

	for _, tc := range tests {
		got := resolveFirstNonEmpty(tc.inputs...)
		if got != tc.want {
			t.Errorf("resolveFirstNonEmpty(%v) = %q, want %q", tc.inputs, got, tc.want)
		}
	}
}

func TestResolveSelector(t *testing.T) {
	if got := resolveSelector("", "button.submit", "", "", "", ""); got != "button.submit" {
		t.Errorf("expected button.submit, got %s", got)
	}
	if got := resolveSelector("", "", "#login", "", "", ""); got != "#login" {
		t.Errorf("expected #login, got %s", got)
	}
	if got := resolveSelector("", "", "", "//button", "", ""); got != "//button" {
		t.Errorf("expected //button, got %s", got)
	}
	if got := resolveSelector("ref-12", "button", "", "", "", ""); got != "ref-12" {
		t.Errorf("expected ref to have precedence, got %s", got)
	}
}

func TestResolveValue(t *testing.T) {
	if got := resolveValue("", "hello text", ""); got != "hello text" {
		t.Errorf("expected text, got %s", got)
	}
	if got := resolveValue("", "", "hello input"); got != "hello input" {
		t.Errorf("expected input, got %s", got)
	}
	if got := resolveValue("primary", "fallback", ""); got != "primary" {
		t.Errorf("expected primary, got %s", got)
	}
}

func TestResolveURL(t *testing.T) {
	if got := resolveURL("http://example.com", "", ""); got != "http://example.com" {
		t.Errorf("expected url, got %s", got)
	}
	if got := resolveURL("", "http://example.org", ""); got != "http://example.org" {
		t.Errorf("expected href fallback, got %s", got)
	}
	if got := resolveURL("", "", "https://sub.domain.com/path"); got != "https://sub.domain.com/path" {
		t.Errorf("expected target URL fallback, got %s", got)
	}
	if got := resolveURL("", "", "localhost:3000"); got != "localhost:3000" {
		t.Errorf("expected localhost fallback, got %s", got)
	}
	if got := resolveURL("", "", "about:blank"); got != "about:blank" {
		t.Errorf("expected about:blank fallback, got %s", got)
	}
	// Target is a tab ID / non-url -> should not be resolved as URL
	if got := resolveURL("", "", "tab-8921B"); got != "" {
		t.Errorf("tab ID should not be resolved as URL, got %s", got)
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"https://example.com", "https://example.com"},
		{"http://example.com", "http://example.com"},
		{"about:blank", "about:blank"},
		{"localhost:3000", "http://localhost:3000"},
		{"127.0.0.1:8080/app", "http://127.0.0.1:8080/app"},
		{"/tmp/index.html", "file:///tmp/index.html"},
		{"google.com", "https://google.com"},
		{"www.google.com/search", "https://www.google.com/search"},
	}
	for _, tc := range tests {
		got := normalizeURL(tc.input)
		if got != tc.want {
			t.Errorf("normalizeURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLooksLikeURL(t *testing.T) {
	valid := []string{
		"http://localhost:3000",
		"https://google.com",
		"file:///tmp/test.html",
		"ws://127.0.0.1:9222/devtools",
		"localhost:8080",
		"/dashboard/settings",
		"custom-scheme://app",
		"about:blank",
		"data:text/html,test",
		"127.0.0.1:3000",
		"www.google.com",
		"example.com",
	}
	for _, v := range valid {
		if !looksLikeURL(v) {
			t.Errorf("looksLikeURL(%q) should be true", v)
		}
	}

	invalid := []string{
		"",
		"   ",
		"btn-submit",
		"tab_1234",
		"C39B72F4",
	}
	for _, inv := range invalid {
		if looksLikeURL(inv) {
			t.Errorf("looksLikeURL(%q) should be false", inv)
		}
	}
}

func TestIsNumericPort(t *testing.T) {
	tests := []struct {
		input    string
		wantPort int
		wantOK   bool
	}{
		{"9222", 9222, true},
		{":8080", 8080, true},
		{" 3000 ", 3000, true},
		{"0", 0, false},
		{"70000", 0, false},
		{"not-a-port", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		port, ok := isNumericPort(tc.input)
		if port != tc.wantPort || ok != tc.wantOK {
			t.Errorf("isNumericPort(%q) = (%d, %v), want (%d, %v)", tc.input, port, ok, tc.wantPort, tc.wantOK)
		}
	}
}

func TestResolveTimeout(t *testing.T) {
	// timeout_ms takes precedence
	if got := resolveTimeout(1500, 5); got != 1500 {
		t.Errorf("expected 1500, got %d", got)
	}
	// timeout <= 60 treated as seconds -> ms
	if got := resolveTimeout(0, 10); got != 10000 {
		t.Errorf("expected 10000, got %d", got)
	}
	// timeout > 60 treated as milliseconds
	if got := resolveTimeout(0, 5000); got != 5000 {
		t.Errorf("expected 5000, got %d", got)
	}
	// zero
	if got := resolveTimeout(0, 0); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestResolveStorageType(t *testing.T) {
	tests := map[string]string{
		"cookies":         "cookie",
		"Cookie":          "cookie",
		"local":           "local",
		"localStorage":    "local",
		"local_storage":   "local",
		"session":         "session",
		"sessionStorage":  "session",
		"session_storage": "session",
		"all":             "all",
		"*":               "all",
		"custom":          "custom",
	}
	for in, want := range tests {
		if got := resolveStorageType(in); got != want {
			t.Errorf("resolveStorageType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAliasJSONUnmarshal tests that JSON payloads from agents using common alias keys unmarshal cleanly
func TestAliasJSONUnmarshal(t *testing.T) {
	// 1. clickInput with aliases: css, target, delay
	{
		raw := `{"css": "#submit-btn", "target": "#submit-btn", "delay": 2, "timeout": 5}`
		var in clickInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal clickInput failed: %v", err)
		}
		if sel := resolveSelector(in.Ref, in.Selector, in.CSS, in.Target, in.XPath, in.Query); sel != "#submit-btn" {
			t.Errorf("resolved selector = %q, want #submit-btn", sel)
		}
		if to := resolveTimeout(in.TimeoutMs, in.Timeout); to != 5000 {
			t.Errorf("resolved timeout = %d, want 5000", to)
		}
		if d := resolveTimeout(in.DelayMs, in.Delay); d != 2000 {
			t.Errorf("resolved delay = %d, want 2000", d)
		}
	}

	// 2. fillInput with aliases: text, input, query
	{
		raw := `{"query": "input[name=username]", "text": "alice"}`
		var in fillInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal fillInput failed: %v", err)
		}
		if sel := resolveSelector(in.Ref, in.Selector, in.CSS, in.Target, in.XPath, in.Query); sel != "input[name=username]" {
			t.Errorf("resolved selector = %q", sel)
		}
		if val := resolveValue(in.Value, in.Text, in.Input); val != "alice" {
			t.Errorf("resolved value = %q, want alice", val)
		}
	}

	// 3. navigateInput with aliases: href, target, reload, timeout
	{
		raw := `{"href": "https://news.ycombinator.com", "reload": true, "timeout": 15}`
		var in navigateInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal navigateInput failed: %v", err)
		}
		if u := resolveURL(in.URL, in.Href, in.Target); u != "https://news.ycombinator.com" {
			t.Errorf("resolved url = %q", u)
		}
		if !in.Reload {
			t.Errorf("expected reload to be true")
		}
		if to := resolveTimeout(in.TimeoutMs, in.Timeout); to != 15000 {
			t.Errorf("resolved timeout = %d, want 15000", to)
		}
	}

	// 4. evalInput with aliases: code, target, timeout
	{
		raw := `{"code": "document.title", "target": "tab-1", "timeout": 3}`
		var in evalInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal evalInput failed: %v", err)
		}
		code := resolveFirstNonEmpty(in.Script, in.Code)
		if code != "document.title" {
			t.Errorf("resolved code = %q, want document.title", code)
		}
		tab := resolveFirstNonEmpty(in.TargetID, in.TabID, in.Target)
		if tab != "tab-1" {
			t.Errorf("resolved tab = %q, want tab-1", tab)
		}
		if to := resolveTimeout(in.TimeoutMs, in.Timeout); to != 3000 {
			t.Errorf("resolved timeout = %d, want 3000", to)
		}
	}

	// 5. storageInput with storage_type alias
	{
		raw := `{"storage_type": "cookies"}`
		var in getStorageInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal getStorageInput failed: %v", err)
		}
		st := resolveStorageType(resolveFirstNonEmpty(in.Type, in.StorageType))
		if st != "cookie" {
			t.Errorf("resolved storage type = %q, want cookie", st)
		}
	}

	// 6. uploadFileInput with single file / path
	{
		raw := `{"file": "/tmp/avatar.png", "target": "input[type=file]"}`
		var in uploadFileInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal uploadFileInput failed: %v", err)
		}
		files := in.Files
		if len(files) == 0 {
			if f := resolveFirstNonEmpty(in.Path, in.File); f != "" {
				files = []string{f}
			}
		}
		if len(files) != 1 || files[0] != "/tmp/avatar.png" {
			t.Errorf("resolved files = %v, want [/tmp/avatar.png]", files)
		}
	}

	// 7. screenshotInput with file, filename, target
	{
		raw := `{"file": "shot.jpg", "target": "tab-A"}`
		var in screenshotInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal screenshotInput failed: %v", err)
		}
		path := resolveInputPath(in.Path, in.FilePath, in.File, in.Filename, in.Output)
		if path != "shot.jpg" {
			t.Errorf("resolved path = %q, want shot.jpg", path)
		}
		tab := resolveFirstNonEmpty(in.TargetID, in.TabID, in.Target)
		if tab != "tab-A" {
			t.Errorf("resolved tab = %q, want tab-A", tab)
		}
	}

	// 8. selectTabInput with url and title
	{
		raw := `{"url": "https://news.ycombinator.com", "title": "Hacker News"}`
		var in selectTabInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal selectTabInput failed: %v", err)
		}
		if in.URL != "https://news.ycombinator.com" || in.Title != "Hacker News" {
			t.Errorf("unexpected selectTabInput values: %+v", in)
		}
	}

	// 9. batchActionStep with href, target, delay
	{
		raw := `{"action": "navigate", "target": "localhost:3000", "delay": 1}`
		var in batchActionStep
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal batchActionStep failed: %v", err)
		}
		url := resolveURL(in.URL, in.Href, in.Target)
		if url != "localhost:3000" {
			t.Errorf("resolved url = %q, want localhost:3000", url)
		}
		if norm := normalizeURL(url); norm != "http://localhost:3000" {
			t.Errorf("normalized url = %q, want http://localhost:3000", norm)
		}
		if to := resolveTimeout(in.DelayMs, in.Delay); to != 1000 {
			t.Errorf("resolved delay = %d, want 1000", to)
		}
	}

	// 10. getStorageInput with name alias
	{
		raw := `{"type": "cookie", "name": "auth_token"}`
		var in getStorageInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal getStorageInput failed: %v", err)
		}
		key := resolveFirstNonEmpty(in.Key, in.Name)
		if key != "auth_token" {
			t.Errorf("resolved key = %q, want auth_token", key)
		}
	}

	// 11. tryUntilInput with code alias
	{
		raw := `{"code": "window.ready === true", "timeout": 5}`
		var in tryUntilInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal tryUntilInput failed: %v", err)
		}
		cond := resolveFirstNonEmpty(in.Condition, in.Script, in.Expression, in.Code)
		if cond != "window.ready === true" {
			t.Errorf("resolved cond = %q", cond)
		}
	}

	// 12. findInput with css, ref, xpath
	{
		raw := `{"css": ".main-title", "xpath": "//h1"}`
		var in findInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal findInput failed: %v", err)
		}
		sel := resolveSelector(in.Ref, in.Selector, in.CSS, in.Target, in.XPath, "")
		if sel != ".main-title" {
			t.Errorf("resolved sel = %q, want .main-title", sel)
		}
	}

	// 13. tabScopeInput absorbs dummy target, tab_id, limit
	{
		raw := `{"target": "tab-123", "limit": 10}`
		var in tabScopeInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal tabScopeInput failed: %v", err)
		}
		if in.Target != "tab-123" || in.Limit != 10 {
			t.Errorf("unexpected tabScopeInput values: %+v", in)
		}
	}
}

func TestUploadFilePathsResolution(t *testing.T) {
	cases := []struct {
		input uploadFileInput
		want  []string
	}{
		{uploadFileInput{Paths: []string{"/tmp/a.png"}}, []string{"/tmp/a.png"}},
		{uploadFileInput{Files: []string{"/tmp/b.png", "/tmp/c.png"}}, []string{"/tmp/b.png", "/tmp/c.png"}},
		{uploadFileInput{File: "/tmp/d.png"}, []string{"/tmp/d.png"}},
		{uploadFileInput{Path: "/tmp/e.png"}, []string{"/tmp/e.png"}},
	}
	for i, tc := range cases {
		paths := tc.input.Paths
		if len(paths) == 0 && len(tc.input.Files) > 0 {
			paths = tc.input.Files
		}
		if len(paths) == 0 {
			if p := resolveFirstNonEmpty(tc.input.Path, tc.input.File); p != "" {
				paths = []string{p}
			}
		}
		if len(paths) != len(tc.want) {
			t.Fatalf("case %d: expected %d paths, got %d", i, len(tc.want), len(paths))
		}
		for j := range paths {
			if paths[j] != tc.want[j] {
				t.Errorf("case %d: path %d = %q, want %q", i, j, paths[j], tc.want[j])
			}
		}
	}
}
