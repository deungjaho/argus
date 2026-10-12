package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// =============================================================================
// Challenge Suite 1: RemoteObject Release Lifecycle in Tool Handlers
// =============================================================================

// cdpLifecycleMockServer handles both CDP HTTP endpoints and WebSocket connections.
type cdpLifecycleMockServer struct {
	server       *httptest.Server
	port         int
	wsURL        string
	calls        []map[string]any
	releaseCalls []string
	callFuncs    []string
	failCallFunc atomic.Bool
	mu           sync.Mutex
}

func newCDPLifecycleMockServer(t *testing.T) *cdpLifecycleMockServer {
	t.Helper()
	ms := &cdpLifecycleMockServer{
		calls:        make([]map[string]any, 0),
		releaseCalls: make([]string, 0),
		callFuncs:    make([]string, 0),
	}

	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HTTP 1.1 /json/version
		if r.URL.Path == "/json/version" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Browser":              "MockChrome/120.0",
				"Protocol-Version":     "1.3",
				"webSocketDebuggerUrl": ms.wsURL,
			})
			return
		}

		// HTTP 1.1 /json/list
		if r.URL.Path == "/json/list" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":                   "test_lifecycle_target",
					"type":                 "page",
					"url":                  "http://example.com/test",
					"webSocketDebuggerUrl": ms.wsURL,
				},
			})
			return
		}

		// WebSocket /devtools/
		if strings.Contains(r.URL.Path, "/devtools/") {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()

			for {
				var req map[string]any
				if err := ws.ReadJSON(&req); err != nil {
					return
				}
				id, _ := req["id"].(float64)
				method, _ := req["method"].(string)
				params, _ := req["params"].(map[string]any)

				ms.mu.Lock()
				ms.calls = append(ms.calls, req)
				if method == "Runtime.releaseObject" {
					if objID, ok := params["objectId"].(string); ok {
						ms.releaseCalls = append(ms.releaseCalls, objID)
					}
				} else if method == "Runtime.callFunctionOn" {
					if objID, ok := params["objectId"].(string); ok {
						ms.callFuncs = append(ms.callFuncs, objID)
					}
				}
				failFunc := ms.failCallFunc.Load()
				ms.mu.Unlock()

				var resp map[string]any
				switch method {
				case "DOM.resolveNode":
					backendNodeID, _ := params["backendNodeId"].(float64)
					resp = map[string]any{
						"id": int(id),
						"result": map[string]any{
							"object": map[string]any{
								"objectId": fmt.Sprintf("mock_obj_%d", int(backendNodeID)),
							},
						},
					}
				case "DOM.getBoxModel":
					resp = map[string]any{
						"id": int(id),
						"result": map[string]any{
							"model": map[string]any{
								"width":   100,
								"height":  40,
								"content": []float64{10, 20, 110, 20, 110, 60, 10, 60},
							},
						},
					}
				case "DOM.scrollIntoViewIfNeeded":
					resp = map[string]any{
						"id":     int(id),
						"result": map[string]any{},
					}
				case "Runtime.callFunctionOn":
					if failFunc {
						resp = map[string]any{
							"id": int(id),
							"result": map[string]any{
								"exceptionDetails": map[string]any{
									"text": "Simulated function exception",
								},
							},
						}
					} else {
						resp = map[string]any{
							"id": int(id),
							"result": map[string]any{
								"result": map[string]any{
									"type":  "object",
									"value": map[string]any{"ok": true, "filled": true},
								},
							},
						}
					}
				case "Runtime.releaseObject":
					resp = map[string]any{
						"id":     int(id),
						"result": map[string]any{},
					}
				case "Runtime.evaluate":
					resp = map[string]any{
						"id": int(id),
						"result": map[string]any{
							"result": map[string]any{
								"type":  "string",
								"value": "mock_eval_success",
							},
						},
					}
				case "Input.insertText":
					resp = map[string]any{
						"id":     int(id),
						"result": map[string]any{},
					}
				default:
					resp = map[string]any{
						"id":     int(id),
						"result": map[string]any{},
					}
				}

				if err := ws.WriteJSON(resp); err != nil {
					return
				}
			}
		}

		http.NotFound(w, r)
	})

	ms.server = httptest.NewServer(handler)
	ms.port = ms.server.Listener.Addr().(*net.TCPAddr).Port
	ms.wsURL = "ws" + strings.TrimPrefix(ms.server.URL, "http") + "/devtools/page/test_lifecycle_target"
	return ms
}

func (ms *cdpLifecycleMockServer) close() {
	ms.server.Close()
}

func (ms *cdpLifecycleMockServer) getReleasedObjects() []string {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	res := make([]string, len(ms.releaseCalls))
	copy(res, ms.releaseCalls)
	return res
}

func setupMockArgusSession(t *testing.T, ms *cdpLifecycleMockServer) (*mcp.ClientSession, func()) {
	t.Helper()
	oldURL, oldPort, oldAttach := getConfig()
	attachTrue := true
	setConfig("http://127.0.0.1:"+strconv.Itoa(ms.port)+"/test", ms.port, &attachTrue)
	cdpPort = ms.port
	configuredPort = ms.port
	setCurrentTargetID("test_lifecycle_target")

	server := mcp.NewServer(&mcp.Implementation{Name: "argus", Version: "dev"}, nil)
	registerElementTools(server)
	registerInteractionTools(server)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-challenger-client", Version: "1.0"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	_, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect failed: %v", err)
	}
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}

	cleanup := func() {
		closeAllConns()
		clearCurrentTargetID()
		if oldPort != 9224 && oldPort != 0 {
			setConfig(oldURL, oldPort, &oldAttach)
			cdpPort = oldPort
			configuredPort = oldPort
		}
	}
	return clientSession, cleanup
}

// TestEmpiricalToolGetElementReleasesRemoteObject verifies that get_element handler
// in tools_element.go always invokes Runtime.releaseObject on resolved nodes.
func TestEmpiricalToolGetElementReleasesRemoteObject(t *testing.T) {
	mock := newCDPLifecycleMockServer(t)
	defer mock.close()

	targetID := "test_lifecycle_target"
	storeA11yTree(targetID, []map[string]any{
		{
			"nodeId":           "101",
			"backendDOMNodeId": 501,
			"role":             map[string]any{"value": "textbox"},
			"name":             map[string]any{"value": "Username"},
		},
	})
	defer clearA11yCache(targetID)

	clientSession, cleanup := setupMockArgusSession(t, mock)
	defer cleanup()

	t.Run("NormalSuccessReleasesRemoteObject", func(t *testing.T) {
		ctx := context.Background()
		res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "get_element",
			Arguments: map[string]any{
				"ref":     "ax:101",
				"include": "all",
			},
		})
		if err != nil {
			t.Fatalf("get_element handler failed: %v", err)
		}
		if res == nil {
			t.Fatalf("expected non-nil result from get_element")
		}

		// Verify releaseObject was called for mock_obj_501
		released := mock.getReleasedObjects()
		if len(released) != 1 || released[0] != "mock_obj_501" {
			t.Errorf("expected ['mock_obj_501'] released, got %v", released)
		}
	})

	t.Run("ErrorPathStillReleasesRemoteObjectViaDefer", func(t *testing.T) {
		mock.mu.Lock()
		mock.releaseCalls = nil
		mock.mu.Unlock()
		mock.failCallFunc.Store(true)
		defer mock.failCallFunc.Store(false)

		ctx := context.Background()
		res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "get_element",
			Arguments: map[string]any{
				"ref":     "ax:101",
				"include": "all",
			},
		})
		if err == nil && (res == nil || !res.IsError) {
			t.Fatalf("expected error when cdpCallFunctionOn fails, got res: %+v", res)
		}

		// Even when error occurs, defer MUST have called Runtime.releaseObject
		released := mock.getReleasedObjects()
		if len(released) != 1 || released[0] != "mock_obj_501" {
			t.Errorf("expected defer to release ['mock_obj_501'] on error, got %v", released)
		}
	})
}

// TestEmpiricalToolFillReleasesRemoteObject verifies that fill handler
// in tools_interaction.go always invokes Runtime.releaseObject on resolved nodes.
func TestEmpiricalToolFillReleasesRemoteObject(t *testing.T) {
	mock := newCDPLifecycleMockServer(t)
	defer mock.close()

	targetID := "test_lifecycle_target"
	storeA11yTree(targetID, []map[string]any{
		{
			"nodeId":           "202",
			"backendDOMNodeId": 602,
			"role":             map[string]any{"value": "combobox"},
			"name":             map[string]any{"value": "Country"},
		},
	})
	defer clearA11yCache(targetID)

	clientSession, cleanup := setupMockArgusSession(t, mock)
	defer cleanup()

	t.Run("FillValueReleasesRemoteObject", func(t *testing.T) {
		mock.mu.Lock()
		mock.releaseCalls = nil
		mock.mu.Unlock()

		ctx := context.Background()
		_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "fill",
			Arguments: map[string]any{
				"ref":       "ax:202",
				"value":     "Canada",
				"no_wait":   true,
				"settle_ms": 1,
			},
		})
		if err != nil {
			t.Fatalf("fill handler failed: %v", err)
		}

		released := mock.getReleasedObjects()
		if len(released) != 1 || released[0] != "mock_obj_602" {
			t.Errorf("expected ['mock_obj_602'] released, got %v", released)
		}
	})

	t.Run("FillValuesSelectReleasesRemoteObject", func(t *testing.T) {
		mock.mu.Lock()
		mock.releaseCalls = nil
		mock.mu.Unlock()

		ctx := context.Background()
		_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "fill",
			Arguments: map[string]any{
				"ref":       "ax:202",
				"values":    []string{"US", "CA"},
				"no_wait":   true,
				"settle_ms": 1,
			},
		})
		if err != nil {
			t.Fatalf("fill handler with values failed: %v", err)
		}

		released := mock.getReleasedObjects()
		if len(released) != 1 || released[0] != "mock_obj_602" {
			t.Errorf("expected ['mock_obj_602'] released on select values, got %v", released)
		}
	})

	t.Run("FillErrorPathReleasesRemoteObject", func(t *testing.T) {
		mock.mu.Lock()
		mock.releaseCalls = nil
		mock.mu.Unlock()
		mock.failCallFunc.Store(true)
		defer mock.failCallFunc.Store(false)

		ctx := context.Background()
		res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "fill",
			Arguments: map[string]any{
				"ref":       "ax:202",
				"value":     "ErrVal",
				"no_wait":   true,
				"settle_ms": 1,
			},
		})
		if err == nil && (res == nil || !res.IsError) {
			t.Fatalf("expected error from fill handler, got res: %+v", res)
		}

		released := mock.getReleasedObjects()
		if len(released) != 1 || released[0] != "mock_obj_602" {
			t.Errorf("expected ['mock_obj_602'] released on fill error, got %v", released)
		}
	})
}

// TestEmpiricalToolTypeReleasesRemoteObject verifies that type handler
// in tools_interaction.go always invokes Runtime.releaseObject on resolved nodes.
func TestEmpiricalToolTypeReleasesRemoteObject(t *testing.T) {
	mock := newCDPLifecycleMockServer(t)
	defer mock.close()

	targetID := "test_lifecycle_target"
	storeA11yTree(targetID, []map[string]any{
		{
			"nodeId":           "303",
			"backendDOMNodeId": 703,
			"role":             map[string]any{"value": "textbox"},
			"name":             map[string]any{"value": "Search"},
		},
	})
	defer clearA11yCache(targetID)

	clientSession, cleanup := setupMockArgusSession(t, mock)
	defer cleanup()

	t.Run("TypeReleasesRemoteObjectAfterExecution", func(t *testing.T) {
		mock.mu.Lock()
		mock.releaseCalls = nil
		mock.mu.Unlock()

		ctx := context.Background()
		_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "type",
			Arguments: map[string]any{
				"ref":     "ax:303",
				"text":    "hello world",
				"no_wait": true,
			},
		})
		if err != nil {
			t.Fatalf("type handler failed: %v", err)
		}

		released := mock.getReleasedObjects()
		if len(released) != 1 || released[0] != "mock_obj_703" {
			t.Errorf("expected ['mock_obj_703'] released, got %v", released)
		}
	})
}

// =============================================================================
// Challenge Suite 2: a11yFillFunction vs fillExpression Complete Parity Oracle
// =============================================================================

// TestEmpiricalFillFunctionParityPhases checks that every critical phase and
// behavior in fillExpression is strictly present in a11yFillFunction.
func TestEmpiricalFillFunctionParityPhases(t *testing.T) {
	requiredPhases := []struct {
		name    string
		pattern string
	}{
		{"Container sniffing: label.control", "el.control"},
		{"Container sniffing: label.htmlFor", "el.htmlFor ? document.getElementById(el.htmlFor)"},
		{"Container sniffing: querySelector inputs", "el.querySelector('input:not([type=\"hidden\"]), textarea, select, [contenteditable=\"true\"]')"},
		{"ScrollIntoView: instant center", "el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'})"},
		{"Focus invocation", "el.focus()"},
		{"Select option uncheck loop", "for (const opt of opts) opt.selected = false;"},
		{"Select option match by value or text", "o.value === text || o.text.trim() === text"},
		{"Select input event dispatch", "el.dispatchEvent(new Event('input', {bubbles: true, composed: true}))"},
		{"Select change event dispatch", "el.dispatchEvent(new Event('change', {bubbles: true, composed: true}))"},
		{"React value tracker capture previousValue", "const previousValue = el.value !== undefined ? el.value : '';"},
		{"React value tracker same-value mismatch", "previousValue === text ? (text === '' ? '__argus_cleared__' : '') : previousValue"},
		{"React value tracker setValue", "el._valueTracker.setValue("},
		{"Beforeinput event name", "'beforeinput'"},
		{"Beforeinput cancelable flag", "cancelable: true"},
		{"Beforeinput inputType insertText", "inputType: 'insertText'"},
		{"Beforeinput data binding", "data: text"},
		{"HTMLTextAreaElement prototype lookup", "window.HTMLTextAreaElement.prototype"},
		{"HTMLInputElement prototype lookup", "window.HTMLInputElement.prototype"},
		{"Property descriptor value setter lookup", "Object.getOwnPropertyDescriptor"},
		{"Descriptor setter invocation", "desc.set.call(el, text)"},
		{"Fallback value assignment", "el.value = text;"},
		{"Input event dispatch with insertText", "new InputEvent('input', {"},
		{"Input event bubbles and composed", "bubbles: true"},
		{"Change event dispatch", "new Event('change', {bubbles: true, composed: true})"},
		{"Contenteditable beforeinput", "el.isContentEditable"},
		{"Contenteditable textContent update", "el.textContent = text;"},
		{"Return object structure", "{filled: true, value: text}"},
	}

	for _, phase := range requiredPhases {
		t.Run(phase.name, func(t *testing.T) {
			if !strings.Contains(a11yFillFunction, phase.pattern) {
				t.Errorf("a11yFillFunction missing required pattern: %q", phase.pattern)
			}
		})
	}
}

// =============================================================================
// Challenge Suite 3: Real Chrome Controlled Form & V8 Lifecycle Stress Test
// =============================================================================

// TestEmpiricalRealChromeControlledFormParityAndV8Lifecycle runs against an
// isolated headless Chrome child process, stress testing React _valueTracker,
// Vue 3 v-model, textarea, select, contenteditable, and confirms that
// Runtime.releaseObject truly frees the remote object in Chrome's V8 engine.
func TestEmpiricalRealChromeControlledFormParityAndV8Lifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping isolated Chrome E2E test in -short mode; run explicitly without -short")
	}

	inst, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	// Red line assertion: host port 9224 must NEVER be touched
	if inst.Port == ForbiddenCDPPort {
		t.Fatalf("CRITICAL RED LINE VIOLATION: Isolated Chrome running on host port %d", ForbiddenCDPPort)
	}

	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("failed to connect to isolated Chrome CDP: %v", err)
	}

	// Load test page with controlled forms and event listeners
	testHTML := `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>
<div id="test-root">
  <!-- 1. Simulated React Controlled Input -->
  <input type="text" id="react-input" value="" />

  <!-- 2. Simulated Vue Controlled Input -->
  <input type="text" id="vue-input" value="" />

  <!-- 3. Textarea -->
  <textarea id="react-textarea"></textarea>

  <!-- 4. Select Dropdown -->
  <select id="test-select">
    <option value="opt1">First Option</option>
    <option value="opt2">Second Option</option>
    <option value="opt3">Third Option</option>
  </select>

  <!-- 5. Contenteditable -->
  <div id="test-contenteditable" contenteditable="true">Initial CE</div>

  <!-- 6. Outer Card / Label Sniffing -->
  <label id="test-label" for="labeled-input"><span>Label Text</span></label>
  <input type="text" id="labeled-input" value="" />
</div>
<script>
  window.eventLog = [];
  function logEvent(elId, e) {
    window.eventLog.push({
      target: elId,
      type: e.type,
      data: e.data !== undefined ? e.data : null,
      inputType: e.inputType !== undefined ? e.inputType : null,
      cancelable: e.cancelable,
      bubbles: e.bubbles,
      valueNow: e.target ? (e.target.value !== undefined ? e.target.value : e.target.textContent) : null
    });
  }

  // Attach listeners to react-input
  const rIn = document.getElementById('react-input');
  // Install React-like _valueTracker
  let trackerVal = '';
  rIn._valueTracker = {
    getValue: function() { return trackerVal; },
    setValue: function(val) { trackerVal = String(val); },
    stop: function() {}
  };
  ['beforeinput', 'input', 'change'].forEach(ev => {
    rIn.addEventListener(ev, e => logEvent('react-input', e));
  });

  // Attach listeners to vue-input
  const vIn = document.getElementById('vue-input');
  window.vueModel = '';
  vIn.addEventListener('input', e => {
    window.vueModel = e.target.value;
    logEvent('vue-input', e);
  });
  vIn.addEventListener('change', e => logEvent('vue-input', e));

  // Attach to textarea
  const txt = document.getElementById('react-textarea');
  ['beforeinput', 'input', 'change'].forEach(ev => {
    txt.addEventListener(ev, e => logEvent('react-textarea', e));
  });

  // Attach to select
  const sel = document.getElementById('test-select');
  ['input', 'change'].forEach(ev => {
    sel.addEventListener(ev, e => logEvent('test-select', e));
  });

  // Attach to contenteditable
  const ce = document.getElementById('test-contenteditable');
  ['beforeinput', 'input', 'change'].forEach(ev => {
    ce.addEventListener(ev, e => logEvent('test-contenteditable', e));
  });
</script>
</body></html>`

	setDocExpr := fmt.Sprintf(`document.open(); document.write(%q); document.close();`, testHTML)
	if _, err := cdpEvaluate(ws, setDocExpr, true); err != nil {
		t.Fatalf("failed to inject test HTML: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// 1. Resolve RemoteObject for #react-input via DOM.resolveNode
	var rootDoc struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := cdpRequest(ws, "DOM.getDocument", map[string]any{"depth": -1}, &rootDoc); err != nil {
		t.Fatalf("DOM.getDocument failed: %v", err)
	}

	var queryRes struct {
		NodeID int `json:"nodeId"`
	}
	if err := cdpRequest(ws, "DOM.querySelector", map[string]any{
		"nodeId":   rootDoc.Root.NodeID,
		"selector": "#react-input",
	}, &queryRes); err != nil {
		t.Fatalf("DOM.querySelector #react-input failed: %v", err)
	}

	var describeRes struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	if err := cdpRequest(ws, "DOM.describeNode", map[string]any{"nodeId": queryRes.NodeID}, &describeRes); err != nil {
		t.Fatalf("DOM.describeNode failed: %v", err)
	}

	objID, err := cdpResolveNode(ws, describeRes.Node.BackendNodeID)
	if err != nil {
		t.Fatalf("cdpResolveNode failed: %v", err)
	}

	// 2. Call a11yFillFunction on the RemoteObject with "Alpha"
	fillRes, err := cdpCallFunctionOn(ws, objID, a11yFillFunction, []any{"Alpha"}, true)
	if err != nil {
		t.Fatalf("a11yFillFunction failed on #react-input: %v", err)
	}
	resMap, ok := fillRes.(map[string]any)
	if !ok || resMap["filled"] != true || resMap["value"] != "Alpha" {
		t.Errorf("unexpected fill result: %+v", fillRes)
	}

	// Check event log order for react-input
	var logs []map[string]any
	logVal, err := cdpEvaluate(ws, "window.eventLog", true)
	if err != nil {
		t.Fatalf("failed to read window.eventLog: %v", err)
	}
	logBytes, _ := json.Marshal(logVal)
	_ = json.Unmarshal(logBytes, &logs)

	if len(logs) < 3 {
		t.Fatalf("expected at least 3 events (beforeinput, input, change), got %d: %+v", len(logs), logs)
	}
	if logs[0]["type"] != "beforeinput" || logs[0]["data"] != "Alpha" || logs[0]["inputType"] != "insertText" {
		t.Errorf("expected beforeinput with data 'Alpha', got %+v", logs[0])
	}
	if logs[1]["type"] != "input" || logs[1]["data"] != "Alpha" || logs[1]["inputType"] != "insertText" {
		t.Errorf("expected input with data 'Alpha', got %+v", logs[1])
	}
	if logs[2]["type"] != "change" {
		t.Errorf("expected change event, got %+v", logs[2])
	}

	// 3. Test React _valueTracker same-value transition
	fillSameRes, err := cdpCallFunctionOn(ws, objID, a11yFillFunction, []any{"Alpha"}, true)
	if err != nil {
		t.Fatalf("a11yFillFunction same value failed: %v", err)
	}
	if sameMap, ok := fillSameRes.(map[string]any); !ok || sameMap["value"] != "Alpha" {
		t.Errorf("unexpected same value fill res: %+v", fillSameRes)
	}

	// 4. Test Vue 3 v-model updates via cdpCallFunctionOn
	var queryVue struct {
		NodeID int `json:"nodeId"`
	}
	_ = cdpRequest(ws, "DOM.querySelector", map[string]any{
		"nodeId":   rootDoc.Root.NodeID,
		"selector": "#vue-input",
	}, &queryVue)
	var describeVue struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	_ = cdpRequest(ws, "DOM.describeNode", map[string]any{"nodeId": queryVue.NodeID}, &describeVue)
	vueObjID, _ := cdpResolveNode(ws, describeVue.Node.BackendNodeID)

	_, err = cdpCallFunctionOn(ws, vueObjID, a11yFillFunction, []any{"VueModelTest"}, true)
	if err != nil {
		t.Fatalf("a11yFillFunction on vue-input failed: %v", err)
	}
	vueModelVal, _ := cdpEvaluate(ws, "window.vueModel", true)
	if vueModelVal != "VueModelTest" {
		t.Errorf("expected window.vueModel = 'VueModelTest', got %v", vueModelVal)
	}

	// 5. Test Textarea multiline fill
	var queryTxt struct {
		NodeID int `json:"nodeId"`
	}
	_ = cdpRequest(ws, "DOM.querySelector", map[string]any{
		"nodeId":   rootDoc.Root.NodeID,
		"selector": "#react-textarea",
	}, &queryTxt)
	var describeTxt struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	_ = cdpRequest(ws, "DOM.describeNode", map[string]any{"nodeId": queryTxt.NodeID}, &describeTxt)
	txtObjID, _ := cdpResolveNode(ws, describeTxt.Node.BackendNodeID)

	multiLineText := "Line 1\nLine 2\nLine 3"
	_, err = cdpCallFunctionOn(ws, txtObjID, a11yFillFunction, []any{multiLineText}, true)
	if err != nil {
		t.Fatalf("a11yFillFunction on textarea failed: %v", err)
	}
	txtVal, _ := cdpEvaluate(ws, "document.getElementById('react-textarea').value", true)
	if txtVal != multiLineText {
		t.Errorf("textarea value = %q, want %q", txtVal, multiLineText)
	}

	// 6. Test Select dropdown option matching
	var querySel struct {
		NodeID int `json:"nodeId"`
	}
	_ = cdpRequest(ws, "DOM.querySelector", map[string]any{
		"nodeId":   rootDoc.Root.NodeID,
		"selector": "#test-select",
	}, &querySel)
	var describeSel struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	_ = cdpRequest(ws, "DOM.describeNode", map[string]any{"nodeId": querySel.NodeID}, &describeSel)
	selObjID, _ := cdpResolveNode(ws, describeSel.Node.BackendNodeID)

	_, err = cdpCallFunctionOn(ws, selObjID, a11yFillFunction, []any{"Third Option"}, true)
	if err != nil {
		t.Fatalf("a11yFillFunction on select failed: %v", err)
	}
	selVal, _ := cdpEvaluate(ws, "document.getElementById('test-select').value", true)
	if selVal != "opt3" {
		t.Errorf("select value = %q, want 'opt3'", selVal)
	}

	// 7. Test Contenteditable
	var queryCE struct {
		NodeID int `json:"nodeId"`
	}
	_ = cdpRequest(ws, "DOM.querySelector", map[string]any{
		"nodeId":   rootDoc.Root.NodeID,
		"selector": "#test-contenteditable",
	}, &queryCE)
	var describeCE struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	_ = cdpRequest(ws, "DOM.describeNode", map[string]any{"nodeId": queryCE.NodeID}, &describeCE)
	ceObjID, _ := cdpResolveNode(ws, describeCE.Node.BackendNodeID)

	_, err = cdpCallFunctionOn(ws, ceObjID, a11yFillFunction, []any{"New Editable Text"}, true)
	if err != nil {
		t.Fatalf("a11yFillFunction on contenteditable failed: %v", err)
	}
	ceVal, _ := cdpEvaluate(ws, "document.getElementById('test-contenteditable').textContent", true)
	if ceVal != "New Editable Text" {
		t.Errorf("contenteditable textContent = %q, want 'New Editable Text'", ceVal)
	}

	// 8. Empirical V8 Release Verification:
	// Explicitly release objID, then callFunctionOn on the released objectId.
	// Chrome V8 MUST return an error indicating the object was destroyed.
	if err := cdpRequest(ws, "Runtime.releaseObject", map[string]any{"objectId": objID}, nil); err != nil {
		t.Fatalf("Runtime.releaseObject failed: %v", err)
	}

	_, postReleaseErr := cdpCallFunctionOn(ws, objID, "function() { return 123; }", nil, true)
	if postReleaseErr == nil {
		t.Fatalf("CRITICAL LEAK: RemoteObject %s remained active in V8 after Runtime.releaseObject!", objID)
	}
	if !strings.Contains(postReleaseErr.Error(), "Could not find object with given id") {
		t.Logf("Post-release call error (expected): %v", postReleaseErr)
	}

	// Clean up remaining test objects
	_ = cdpRequest(ws, "Runtime.releaseObject", map[string]any{"objectId": vueObjID}, nil)
	_ = cdpRequest(ws, "Runtime.releaseObject", map[string]any{"objectId": txtObjID}, nil)
	_ = cdpRequest(ws, "Runtime.releaseObject", map[string]any{"objectId": selObjID}, nil)
	_ = cdpRequest(ws, "Runtime.releaseObject", map[string]any{"objectId": ceObjID}, nil)
}
