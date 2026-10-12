//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// startE2EFixtureServer creates and starts an httptest.Server serving all HTML testbeds and API endpoints.
// It registers its teardown via t.Cleanup().
func startE2EFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// Fixture HTML Pages
	mux.HandleFunc("/fixtures/controlled-react", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlReactControlled))
	})

	mux.HandleFunc("/fixtures/controlled-vue", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlVueControlled))
	})

	mux.HandleFunc("/fixtures/a11y-tree", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlA11yTree))
	})

	mux.HandleFunc("/fixtures/dialogs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlDialogs))
	})

	mux.HandleFunc("/fixtures/overlays/vite", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlViteOverlay))
	})

	mux.HandleFunc("/fixtures/overlays/nextjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlNextOverlay))
	})

	mux.HandleFunc("/fixtures/overlays/webpack", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlWebpackOverlay))
	})

	mux.HandleFunc("/fixtures/screenshot-box", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlScreenshotBox))
	})

	mux.HandleFunc("/fixtures/api-mock", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlAPIMock))
	})

	mux.HandleFunc("/fixtures/act-and-verify", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlActAndVerify))
	})

	mux.HandleFunc("/fixtures/complex-app", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlComplexApp))
	})

	// Default API Backend Endpoints (real responses when unmocked, or baseline for mock tests)
	mux.HandleFunc("/api/v1/user/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     42,
			"name":   "Real Backend User",
			"mocked": false,
		})
	})

	mux.HandleFunc("/api/v2/products/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"product": "Default Unmocked Product",
			"status":  "in_stock",
		})
	})

	mux.HandleFunc("/api/checkout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"order_id": "REAL-ORD-1001",
			"status":   "paid",
		})
	})

	mux.HandleFunc("/api/orders/commit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "committed",
			"tx_id":  "tx_998811",
		})
	})

	mux.HandleFunc("/api/v1/telemetry", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"telemetry": "received"})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		server.Close()
	})
	return server
}

const htmlReactControlled = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>React 18 Controlled Component Fixture</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; padding: 24px; }
    .form-group { margin-bottom: 16px; }
    label { display: block; margin-bottom: 6px; font-weight: 600; }
    input { width: 280px; padding: 8px 12px; border: 1px solid #ccc; border-radius: 4px; font-size: 14px; }
    input:focus { border-color: #0066cc; outline: none; }
    .btn { padding: 8px 16px; background: #0066cc; color: #fff; border: none; border-radius: 4px; cursor: pointer; }
    .state-panel { margin-top: 20px; padding: 12px; background: #f5f5f5; border-radius: 4px; font-family: monospace; font-size: 13px; }
    .status-badge { display: inline-block; padding: 4px 8px; border-radius: 3px; font-weight: bold; margin-top: 10px; }
    .status-idle { background: #e0e0e0; color: #333; }
    .status-success { background: #d4edda; color: #155724; }
    .status-error { background: #f8d7da; color: #721c24; }
  </style>
</head>
<body>
  <h2>React 18 Controlled Input Testbed</h2>
  <form id="react-form" onsubmit="return false;">
    <div class="form-group">
      <label for="react-username">Username (Controlled):</label>
      <input type="text" id="react-username" name="username" data-testid="react-username" placeholder="Enter username" autocomplete="off" />
    </div>
    <div class="form-group">
      <label for="react-password">Password (Controlled):</label>
      <input type="password" id="react-password" name="password" data-testid="react-password" placeholder="Enter password" autocomplete="off" />
    </div>
    <div class="form-group">
      <label for="react-company">Company:</label>
      <input type="text" id="react-company" name="company" placeholder="Company Name" autocomplete="off" />
    </div>
    <button type="button" id="react-submit-btn" class="btn" data-testid="react-submit">Sign In</button>
  </form>

  <div class="state-panel">
    <div>State Username: <span id="state-username">(empty)</span></div>
    <div>State Password: <span id="state-password">(empty)</span></div>
    <div>State Company: <span id="state-company">(empty)</span></div>
    <div>Form Dirty: <span id="state-dirty">false</span></div>
    <div>Events Fired: <span id="state-events">none</span></div>
    <div id="submission-status" class="status-badge status-idle">IDLE</div>
  </div>

  <script>
    const reactState = {
      username: '',
      password: '',
      company: '',
      isDirty: false,
      events: []
    };

    function attachReactControl(inputEl, fieldKey) {
      let trackerVal = '';
      inputEl._valueTracker = {
        setValue: function(v) { trackerVal = v; }
      };

      const nativeDescriptor = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value');
      Object.defineProperty(inputEl, 'value', {
        get: function() {
          return nativeDescriptor.get.call(this);
        },
        set: function(val) {
          nativeDescriptor.set.call(this, val);
        },
        configurable: true
      });

      ['beforeinput', 'input', 'change'].forEach(evtName => {
        inputEl.addEventListener(evtName, (e) => {
          reactState.events.push(fieldKey + ':' + evtName);
          document.getElementById('state-events').textContent = reactState.events.slice(-6).join(', ');
        });
      });

      inputEl.addEventListener('input', (e) => {
        reactState[fieldKey] = inputEl.value;
        reactState.isDirty = true;
        document.getElementById('state-' + fieldKey).textContent = inputEl.value || '(empty)';
        document.getElementById('state-dirty').textContent = 'true';
      });
    }

    attachReactControl(document.getElementById('react-username'), 'username');
    attachReactControl(document.getElementById('react-password'), 'password');
    attachReactControl(document.getElementById('react-company'), 'company');

    document.getElementById('react-submit-btn').addEventListener('click', () => {
      const statusEl = document.getElementById('submission-status');
      if (reactState.username === 'admin' && reactState.password === 'secret123') {
        statusEl.className = 'status-badge status-success';
        statusEl.textContent = 'AUTH_SUCCESS: admin';
      } else if (reactState.username) {
        statusEl.className = 'status-badge status-success';
        statusEl.textContent = 'USER_SUBMITTED: ' + reactState.username;
      } else {
        statusEl.className = 'status-badge status-error';
        statusEl.textContent = 'AUTH_FAILED: invalid credentials';
      }
    });
  </script>
</body>
</html>`

const htmlVueControlled = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Vue 3 & Element Plus Form Fixture</title>
  <style>
    body { font-family: sans-serif; padding: 24px; }
    .el-form-item { margin-bottom: 18px; }
    .el-input { position: relative; display: inline-block; width: 300px; border: 1px solid #dcdfe6; border-radius: 4px; padding: 1px; }
    .el-input__inner { width: 100%; border: none; outline: none; padding: 8px 12px; box-sizing: border-box; font-size: 14px; }
    .el-checkbox { display: inline-flex; align-items: center; cursor: pointer; user-select: none; }
    .el-checkbox__input { position: relative; width: 16px; height: 16px; border: 1px solid #dcdfe6; border-radius: 2px; margin-right: 8px; }
    .el-checkbox__input.is-checked { background: #409eff; border-color: #409eff; }
    .el-checkbox__original { opacity: 0; position: absolute; margin: 0; width: 0; height: 0; z-index: -1; }
    .card-wrapper { border: 1px solid #ebeef5; padding: 16px; border-radius: 6px; box-shadow: 0 2px 12px rgba(0,0,0,0.1); width: 360px; }
    .ant-checkbox-wrapper { display: inline-flex; align-items: center; cursor: pointer; }
    .rich-editor { border: 1px solid #ccc; min-height: 40px; padding: 8px; border-radius: 4px; }
  </style>
</head>
<body>
  <div class="card-wrapper">
    <h3>Element Plus Component Demo</h3>
    <form id="vue-form">
      <div class="el-form-item">
        <label>Account Name (Outer Container):</label>
        <div class="el-input" id="account-container" data-testid="el-account-container">
          <input class="el-input__inner" id="account-inner" placeholder="Enter account name" autocomplete="off" />
        </div>
      </div>

      <div class="el-form-item">
        <label for="contact-email">Contact Email Label:</label>
        <input id="contact-email" placeholder="Email through label" />
      </div>

      <div class="el-form-item">
        <label class="el-checkbox" id="remember-wrapper" data-testid="el-remember-wrapper">
          <span class="el-checkbox__input" id="checkbox-box">
            <input type="checkbox" class="el-checkbox__original" id="remember-input" />
          </span>
          <span class="el-checkbox__label">Remember Login Credentials</span>
        </label>
      </div>

      <div class="el-form-item">
        <label class="ant-checkbox-wrapper" id="ant-agree-wrapper">
          <input type="checkbox" id="ant-agree-input" style="opacity:0; position:absolute;" />
          <span>Agree to terms and conditions</span>
        </label>
      </div>

      <div class="el-form-item">
        <label>Rich Contenteditable Editor:</label>
        <div class="rich-editor" id="rich-editor" contenteditable="true">Initial content</div>
      </div>

      <button type="button" id="vue-submit-btn" style="padding: 8px 16px; background: #409eff; color: #fff; border: none; border-radius: 4px; cursor: pointer;">
        Submit Vue Form
      </button>
    </form>

    <div style="margin-top: 16px; padding: 10px; background: #f0f9eb; border: 1px solid #e1f3d8; border-radius: 4px;">
      <div>Reactive Account: <span id="vue-account-val">none</span></div>
      <div>Remember Checked: <span id="vue-remember-val">false</span></div>
      <div>Agree Checked: <span id="vue-agree-val">false</span></div>
      <div>Editor Content: <span id="vue-editor-val">Initial content</span></div>
      <div>Form State: <span id="vue-status">UNCOMMITTED</span></div>
    </div>
  </div>

  <script>
    const vueData = {
      account: '',
      remember: false,
      agree: false,
      editor: 'Initial content'
    };

    const inputInner = document.getElementById('account-inner');
    inputInner.addEventListener('input', (e) => {
      vueData.account = e.target.value;
      document.getElementById('vue-account-val').textContent = e.target.value || 'none';
    });

    const rememberInput = document.getElementById('remember-input');
    const checkboxBox = document.getElementById('checkbox-box');

    rememberInput.addEventListener('change', () => {
      vueData.remember = rememberInput.checked;
      checkboxBox.classList.toggle('is-checked', rememberInput.checked);
      document.getElementById('vue-remember-val').textContent = rememberInput.checked ? 'true' : 'false';
    });

    const antWrapper = document.getElementById('ant-agree-wrapper');
    const antInput = document.getElementById('ant-agree-input');
    antWrapper.addEventListener('click', (e) => {
      if (e.target !== antInput) {
        antInput.checked = !antInput.checked;
      }
      vueData.agree = antInput.checked;
      document.getElementById('vue-agree-val').textContent = antInput.checked ? 'true' : 'false';
    });

    const richEditor = document.getElementById('rich-editor');
    richEditor.addEventListener('input', () => {
      vueData.editor = richEditor.innerText || richEditor.textContent;
      document.getElementById('vue-editor-val').textContent = vueData.editor;
    });

    document.getElementById('vue-submit-btn').addEventListener('click', () => {
      if (vueData.account.length >= 3) {
        document.getElementById('vue-status').textContent = 'COMMITTED: ' + vueData.account;
      } else {
        document.getElementById('vue-status').textContent = 'VALIDATION_FAILED';
      }
    });
  </script>
</body>
</html>`

const htmlA11yTree = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Accessible DOM Tree & Shadow DOM Fixture</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; padding: 24px; }
    .a11y-card { border: 1px solid #ccc; padding: 16px; border-radius: 8px; margin-bottom: 20px; width: 420px; }
    .custom-btn { display: inline-block; padding: 8px 16px; background: #28a745; color: white; border-radius: 4px; cursor: pointer; user-select: none; }
    .custom-input { border: 2px solid #555; padding: 6px 10px; border-radius: 4px; min-width: 200px; }
    .sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0,0,0,0); border: 0; }
  </style>
</head>
<body>
  <h2>Accessibility Tree Reference Testbed</h2>

  <div class="a11y-card" role="region" aria-label="Order Management Region">
    <h3 id="order-title">Customer Order Form</h3>
    <div id="order-desc" class="sr-only">Fill customer information and confirm order placement</div>

    <div style="margin-bottom: 12px;">
      <span id="cust-label">Customer Name:</span>
      <input type="text" id="a11y-cust-name" class="custom-input"
             aria-labelledby="cust-label" aria-describedby="order-desc"
             data-testid="a11y-name-field" value="John Doe" />
    </div>

    <div style="margin-bottom: 12px;">
      <div role="button" tabindex="0" id="a11y-confirm-btn" class="custom-btn"
           aria-label="Confirm and Place Order" aria-haspopup="dialog" data-testid="a11y-confirm-button">
        Confirm Order
      </div>
    </div>

    <div style="margin-bottom: 12px;">
      <button id="hover-target-btn" class="custom-btn" style="background:#17a2b8;">Hover Over Me</button>
      <div id="hover-dropdown" style="display:none; margin-top:8px; padding:8px; background:#fff3cd; border:1px solid #ffeeba;">Dropdown Revealed!</div>
    </div>

    <div aria-hidden="true" id="decorative-element" style="display: none;">Invisible Decorative Text</div>
  </div>

  <accessible-badge id="shadow-badge"></accessible-badge>

  <div id="a11y-action-log" style="padding: 10px; background: #eef2ff; border-radius: 4px; font-family: monospace;">
    Last Interaction: none
  </div>

  <script>
    class AccessibleBadge extends HTMLElement {
      connectedCallback() {
        const shadow = this.attachShadow({ mode: 'open' });
        shadow.innerHTML = ` + "`" + `
          <style>
            .badge-box { border: 1px dashed #6f42c1; padding: 12px; margin-top: 10px; border-radius: 6px; }
            .shadow-btn { background: #6f42c1; color: white; border: none; padding: 6px 12px; border-radius: 4px; cursor: pointer; }
          </style>
          <div class="badge-box" role="group" aria-label="Shadow DOM Subsystem">
            <p>Shadow Root Container</p>
            <button id="shadow-inner-btn" class="shadow-btn" aria-label="Activate Shadow Feature">
              Execute Shadow Action
            </button>
          </div>
        ` + "`" + `;
        shadow.getElementById('shadow-inner-btn').addEventListener('click', () => {
          document.getElementById('a11y-action-log').textContent = 'SHADOW_BUTTON_CLICKED';
        });
      }
    }
    customElements.define('accessible-badge', AccessibleBadge);

    document.getElementById('a11y-confirm-btn').addEventListener('click', () => {
      document.getElementById('a11y-action-log').textContent = 'CONFIRM_BUTTON_CLICKED';
      window.location.hash = '#order-placed';
      const badge = document.createElement('span');
      badge.className = 'confirm-badge';
      badge.textContent = 'Order Confirmed';
      document.body.appendChild(badge);
    });

    document.getElementById('a11y-cust-name').addEventListener('input', (e) => {
      document.getElementById('a11y-action-log').textContent = 'NAME_EDITED: ' + e.target.value;
    });

    const hoverBtn = document.getElementById('hover-target-btn');
    const dropdown = document.getElementById('hover-dropdown');
    hoverBtn.addEventListener('mouseenter', () => {
      dropdown.style.display = 'block';
      document.getElementById('a11y-action-log').textContent = 'HOVERED_SUCCESS';
    });
  </script>
</body>
</html>`

const htmlDialogs = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Native JavaScript Dialogs Fixture</title>
  <style>
    body { font-family: sans-serif; padding: 24px; }
    .btn-group { display: flex; gap: 10px; margin-bottom: 20px; }
    button { padding: 10px 18px; font-size: 14px; border: 1px solid #888; border-radius: 4px; cursor: pointer; background: #fafafa; }
    button:hover { background: #eee; }
    .result-box { padding: 14px; background: #f8f9fa; border: 1px solid #ddd; border-radius: 4px; font-family: monospace; }
  </style>
</head>
<body>
  <h2>Native JS Dialog Anti-Deadlock Testbed</h2>

  <div class="btn-group">
    <button id="trigger-alert" data-testid="btn-alert">Trigger Alert</button>
    <button id="trigger-confirm" data-testid="btn-confirm">Trigger Confirm</button>
    <button id="trigger-prompt" data-testid="btn-prompt">Trigger Prompt</button>
    <button id="trigger-cascade" data-testid="btn-cascade">Trigger Rapid Alert+Confirm</button>
  </div>

  <div class="result-box">
    <div>Alert Log: <span id="alert-log">none</span></div>
    <div>Confirm Result: <span id="confirm-result">none</span></div>
    <div>Prompt Result: <span id="prompt-result">none</span></div>
    <div>Cascade State: <span id="cascade-state">none</span></div>
  </div>

  <script>
    document.getElementById('trigger-alert').addEventListener('click', () => {
      window.alert('CRITICAL_SYSTEM_ALERT: Memory limit reached');
      document.getElementById('alert-log').textContent = 'ALERT_DISMISSED';
    });

    document.getElementById('trigger-confirm').addEventListener('click', () => {
      const res = window.confirm('CONFIRMATION_REQUIRED: Delete all user records?');
      document.body.dataset.ans = res ? 'true' : 'false';
      document.getElementById('confirm-result').textContent = res ? 'CONFIRMED' : 'DISMISSED';
    });

    document.getElementById('trigger-prompt').addEventListener('click', () => {
      const token = window.prompt('PROMPT_INPUT: Enter MFA Security Token', 'default_token_99');
      document.body.dataset.name = token;
      document.getElementById('prompt-result').textContent = token === null ? 'PROMPT_CANCELLED' : ('TOKEN:' + token);
    });

    document.getElementById('trigger-cascade').addEventListener('click', () => {
      document.getElementById('cascade-state').textContent = 'STEP_1_ALERT';
      window.alert('CASCADE_STEP_1');
      document.getElementById('cascade-state').textContent = 'STEP_2_CONFIRM';
      const c = window.confirm('CASCADE_STEP_2_PROCEED?');
      document.getElementById('cascade-state').textContent = c ? 'CASCADE_SUCCESS' : 'CASCADE_ABORTED';
    });
  </script>
</body>
</html>`

const htmlViteOverlay = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Vite Dev Error Overlay Fixture</title>
  <style>
    body { font-family: monospace; padding: 20px; }
  </style>
</head>
<body>
  <h1>App Shell Loading...</h1>
  <div id="app-root">Initializing application state...</div>

  <script>
    class ViteErrorOverlay extends HTMLElement {
      connectedCallback() {
        const shadow = this.attachShadow({ mode: 'open' });
        shadow.innerHTML = ` + "`" + `
          <style>
            :host { position: fixed; top: 0; left: 0; width: 100vw; height: 100vh; background: rgba(0,0,0,0.85); color: #ff6b6b; z-index: 99999; display: flex; flex-direction: column; padding: 40px; box-sizing: border-box; }
            .message-body { font-size: 20px; font-weight: bold; margin-bottom: 12px; }
            .file { color: #ffe066; font-size: 15px; margin-bottom: 8px; }
            .frame { background: #1a1a1a; padding: 12px; border-radius: 4px; color: #f8f9fa; white-space: pre; font-size: 13px; line-height: 1.4; }
            .stack { color: #adb5bd; margin-top: 12px; white-space: pre; font-size: 12px; }
          </style>
          <div class="backdrop">
            <div class="message-body">[plugin:vite:esbuild] Unexpected token</div>
            <div class="file">src/components/UserProfile.tsx:42:15</div>
            <div class="frame">40 |   const user = useUser();
41 |   return (
42 |     <div>{user.name.toUpperCase()}</div>
   |                     ^
43 |   );</div>
            <div class="stack">TypeError: Cannot read properties of undefined (reading 'toUpperCase')
    at UserProfile (src/components/UserProfile.tsx:42:15)</div>
          </div>
        ` + "`" + `;
      }
    }
    customElements.define('vite-error-overlay', ViteErrorOverlay);
    const overlay = document.createElement('vite-error-overlay');
    document.body.appendChild(overlay);
  </script>
</body>
</html>`

const htmlNextOverlay = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Next.js Error Overlay Fixture</title>
</head>
<body>
  <div data-nextjs-dialog="true" class="nextjs-container-errors" style="position:fixed; top:0; left:0; width:100%; height:100%; background:rgba(0,0,0,0.8); z-index:99999; color:white; padding:30px;">
    <h2>Unhandled Runtime Error</h2>
    <div data-nextjs-dialog-body="true">
      <pre>ReferenceError: React is not defined
    at HomePage (pages/index.js:12:4)</pre>
    </div>
  </div>
</body>
</html>`

const htmlWebpackOverlay = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Webpack DevServer Error Overlay Fixture</title>
</head>
<body>
  <webpack-dev-server-client-overlay style="position:fixed; top:0; left:0; width:100%; height:100%; background:black; color:red; padding:30px; z-index:99999;">
    <h2>Failed to compile.</h2>
    <pre>./src/index.js
Module not found: Can't resolve './missing-module' in '/app/src'</pre>
  </webpack-dev-server-client-overlay>
</body>
</html>`

const htmlScreenshotBox = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Element Screenshot Bounding Box Fixture</title>
  <style>
    body { margin: 0; padding: 80px 24px 24px 24px; font-family: sans-serif; background: #e9ecef; }
    .fixed-header { position: fixed; top: 0; left: 0; width: 100%; height: 60px; background: #212529; color: white; display: flex; align-items: center; padding: 0 20px; z-index: 1000; }
    .box-standard { width: 320px; height: 180px; background: #ffffff; border: 4px solid #0d6efd; border-radius: 8px; padding: 16px; box-sizing: border-box; margin-bottom: 24px; box-shadow: 0 4px 8px rgba(0,0,0,0.1); }
    .box-transformed { width: 280px; height: 160px; background: #fff3cd; border: 3px solid #ffc107; padding: 12px; transform: rotate(3deg); margin: 30px 0; }
    .scroll-container { width: 360px; height: 220px; overflow-y: auto; border: 2px solid #6c757d; background: white; padding: 10px; }
    .scroll-spacer { height: 600px; background: repeating-linear-gradient(45deg, #f8f9fa, #f8f9fa 10px, #e9ecef 10px, #e9ecef 20px); }
    .scroll-target { height: 100px; background: #d1e7dd; border: 2px solid #198754; padding: 10px; font-weight: bold; }
  </style>
</head>
<body>
  <div id="card-fixed-header" class="fixed-header">
    <h3>Fixed Application Navbar (100% x 60px)</h3>
  </div>

  <div id="card-standard" data-testid="target-standard" class="box-standard">
    <h4 style="margin-top:0; color: #0d6efd;">Standard Card Target</h4>
    <p>Target for exact bounding box clipping test. Width=320, Height=180.</p>
    <button id="standard-btn">Active Button</button>
  </div>

  <div id="card-transformed" data-testid="target-transformed" class="box-transformed">
    <h4>Transformed Target (rotate 3deg)</h4>
    <p>Verifies box model quadrilateral rotation calculation.</p>
  </div>

  <div class="scroll-container" id="scroll-box">
    <div class="scroll-spacer">Scroll Down To Reveal Target</div>
    <div id="card-scrollable-target" class="scroll-target">
      Deep Overflow Target (Y = 600px)
    </div>
  </div>
  <script>window.requestAnimationFrame = function(cb) { return setTimeout(cb, 10); };</script>
</body>
</html>`

const htmlAPIMock = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Mock Route Interception Fixture</title>
  <style>
    body { font-family: monospace; padding: 24px; }
    .trigger-panel { display: flex; flex-direction: column; gap: 8px; width: 360px; margin-bottom: 20px; }
    button { padding: 8px 12px; cursor: pointer; text-align: left; }
    .response-view { border: 1px solid #444; background: #1e1e1e; color: #d4d4d4; padding: 16px; border-radius: 4px; }
    .badge { display: inline-block; padding: 2px 6px; border-radius: 3px; font-weight: bold; }
    .status-200 { background: #0f5132; color: #d1e7dd; }
    .status-err { background: #842029; color: #f8d7da; }
  </style>
</head>
<body>
  <h2>Network Interception & mock_route Testbed</h2>

  <div class="trigger-panel">
    <button id="btn-fetch-user" data-testid="fetch-user">1. Fetch User 42 (GET /api/v1/user/42)</button>
    <button id="btn-fetch-glob" data-testid="fetch-glob">2. Fetch Product Glob (GET /api/v2/products/...)</button>
    <button id="btn-post-checkout" data-testid="post-checkout">3. Checkout (POST /api/checkout)</button>
    <button id="btn-block-telemetry" data-testid="block-telemetry">4. Block Telemetry (/api/v1/telemetry)</button>
  </div>

  <div class="response-view">
    <div>HTTP Status: <span id="mock-status">none</span></div>
    <div>Header X-Mock: <span id="mock-header">none</span></div>
    <div>Response Body: <pre id="mock-body">{}</pre></div>
  </div>

  <script>
    async function executeFetch(url, options = {}) {
      const statusEl = document.getElementById('mock-status');
      const headerEl = document.getElementById('mock-header');
      const bodyEl = document.getElementById('mock-body');
      try {
        const res = await fetch(url, options);
        statusEl.textContent = res.status + ' ' + res.statusText;
        statusEl.className = res.ok ? 'badge status-200' : 'badge status-err';
        headerEl.textContent = res.headers.get('x-custom-auth') || res.headers.get('x-mock-engine') || res.headers.get('content-type') || 'none';
        const txt = await res.text();
        try {
          bodyEl.textContent = JSON.stringify(JSON.parse(txt), null, 2);
        } catch {
          bodyEl.textContent = txt;
        }
      } catch (err) {
        statusEl.textContent = 'NETWORK_ERROR: ' + err.message;
        statusEl.className = 'badge status-err';
        bodyEl.textContent = 'BLOCKED_OR_FAILED';
      }
    }

    document.getElementById('btn-fetch-user').addEventListener('click', () => {
      executeFetch('/api/v1/user/42');
    });

    document.getElementById('btn-fetch-glob').addEventListener('click', () => {
      executeFetch('/api/v2/products/electronics/item-999');
    });

    document.getElementById('btn-post-checkout').addEventListener('click', () => {
      executeFetch('/api/checkout', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ cart_id: 'cart-1234', total: 99.50 })
      });
    });

    document.getElementById('btn-block-telemetry').addEventListener('click', () => {
      executeFetch('/api/v1/telemetry');
    });
  </script>
</body>
</html>`

const htmlActAndVerify = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Act-and-Verify Feedback Testbed</title>
  <style>
    body { font-family: sans-serif; padding: 24px; }
    .card { border: 1px solid #ccc; padding: 16px; width: 340px; border-radius: 6px; }
    .spinner { color: #888; font-style: italic; }
  </style>
</head>
<body>
  <div class="card">
    <h3>Order Finalization</h3>
    <div id="pending-indicator" class="spinner">Awaiting confirmation...</div>
    <div id="injected-container"></div>
    <button id="action-trigger" style="margin-top: 12px; padding: 8px 16px; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer;">
      Confirm & Finalize
    </button>
  </div>

  <script>
    document.getElementById('action-trigger').addEventListener('click', () => {
      // 1. Navigation
      history.pushState({ step: 2 }, 'Confirmed', '#order-confirmed');

      // 2. Network API
      fetch('/api/orders/commit', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ confirmed: true })
      }).catch(() => {});

      // 3. DOM Mutations (+3 added, -1 removed)
      const spinner = document.getElementById('pending-indicator');
      if (spinner) spinner.remove();

      const container = document.getElementById('injected-container');
      const b1 = document.createElement('div'); b1.textContent = 'Badge 1: Payment Secured';
      const b2 = document.createElement('div'); b2.textContent = 'Badge 2: Receipt Emailed';
      const b3 = document.createElement('div'); b3.textContent = 'Badge 3: Tracking #998811';
      container.appendChild(b1);
      container.appendChild(b2);
      container.appendChild(b3);
    });
  </script>
</body>
</html>`

const htmlComplexApp = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Complex Application SPA Testbed</title>
  <style>
    body { font-family: sans-serif; padding: 24px; }
    .nav { display: flex; gap: 12px; margin-bottom: 20px; }
    .nav a { text-decoration: none; color: #007bff; cursor: pointer; }
    .page-section { border: 1px solid #ddd; padding: 20px; border-radius: 6px; }
    .product-list { display: flex; gap: 16px; margin: 16px 0; }
    .product-card { border: 1px solid #ccc; padding: 12px; border-radius: 4px; width: 140px; cursor: pointer; }
    .error-banner { background: #f8d7da; color: #721c24; padding: 10px; border-radius: 4px; margin-bottom: 12px; }
  </style>
</head>
<body>
  <h2>Enterprise Complex SPA</h2>
  <div class="nav">
    <a id="nav-login" onclick="showSection('login')">Login</a>
    <a id="nav-store" onclick="showSection('store')">Store</a>
    <a id="nav-dashboard" onclick="showSection('dashboard')">Dashboard</a>
  </div>

  <div id="sec-login" class="page-section">
    <h3>User Authentication</h3>
    <input id="login-user" placeholder="Username" /><br/><br/>
    <input id="login-pass" type="password" placeholder="Password" /><br/><br/>
    <button id="login-btn" onclick="performLogin()">Sign In</button>
    <div id="login-msg" style="margin-top:10px;"></div>
  </div>

  <div id="sec-store" class="page-section" style="display:none;">
    <h3>Product Catalog</h3>
    <div class="product-list">
      <div class="product-card" id="card-101" data-id="101" onclick="selectProduct(101)">
        <h4>Item 101</h4>
        <p>$25.00</p>
      </div>
      <div class="product-card" id="card-102" data-id="102" onclick="selectProduct(102)">
        <h4>Item 102</h4>
        <p>$40.00</p>
      </div>
    </div>
    <div>Selected: <span id="store-selected">none</span></div>
    <input id="store-qty" type="number" value="1" style="width:60px;" />
    <button id="store-add-cart" onclick="addToCart()">Add to Cart</button>
    <button id="store-checkout" onclick="performCheckout()">Checkout</button>
    <div id="store-cart-status" style="margin-top:10px;">Cart: 0 items</div>
    <div id="order-success" style="display:none; color:green; font-weight:bold; margin-top:10px;"></div>
  </div>

  <div id="sec-dashboard" class="page-section" style="display:none;">
    <h3>Micro-Frontend Diagnostics</h3>
    <div id="widget-error" class="error-banner" style="display:none;">Widget Error: 500 Network Failure</div>
    <div id="widget-content">Widget Loaded Successfully</div>
    <button id="widget-retry" onclick="retryWidget()">Retry Load</button>
  </div>

  <script>
    function showSection(name) {
      document.getElementById('sec-login').style.display = name === 'login' ? 'block' : 'none';
      document.getElementById('sec-store').style.display = name === 'store' ? 'block' : 'none';
      document.getElementById('sec-dashboard').style.display = name === 'dashboard' ? 'block' : 'none';
    }

    async function performLogin() {
      const user = document.getElementById('login-user').value;
      const pass = document.getElementById('login-pass').value;
      try {
        const res = await fetch('/api/v1/auth/login', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({user, pass})
        });
        if (res.ok) {
          const data = await res.json();
          localStorage.setItem('jwt_token', data.token || 'JWT_DEFAULT');
          document.getElementById('login-msg').textContent = 'SUCCESS: ' + (data.user || user);
          showSection('store');
        } else {
          document.getElementById('login-msg').textContent = 'FAILED';
        }
      } catch (err) {
        document.getElementById('login-msg').textContent = 'ERROR';
      }
    }

    let currentItem = 0;
    let cartCount = 0;
    function selectProduct(id) {
      currentItem = id;
      document.getElementById('store-selected').textContent = 'Item ' + id;
    }

    function addToCart() {
      const qty = parseInt(document.getElementById('store-qty').value, 10) || 1;
      cartCount += qty;
      document.getElementById('store-cart-status').textContent = 'Cart: ' + cartCount + ' items';
    }

    async function performCheckout() {
      const confirmed = window.confirm('Confirm order of ' + cartCount + ' items?');
      if (!confirmed) return;
      try {
        const res = await fetch('/api/checkout', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({count: cartCount})
        });
        const data = await res.json();
        const successEl = document.getElementById('order-success');
        successEl.style.display = 'block';
        successEl.textContent = 'Order Confirmed: ' + (data.order_id || 'ORD-999');
      } catch (err) {}
    }

    function retryWidget() {
      document.getElementById('widget-error').style.display = 'none';
      document.getElementById('widget-content').textContent = 'Widget Recovered & Active';
    }
  </script>
</body>
</html>`
