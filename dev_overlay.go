package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DevOverlayError represents a detected framework error overlay on the page.
type DevOverlayError struct {
	Detected  bool   `json:"detected"`
	Framework string `json:"framework"`            // "vite", "nextjs", "webpack", "react_error_boundary", "generic"
	Title     string `json:"title,omitempty"`      // e.g. "Build Error", "Failed to resolve import"
	Message   string `json:"message"`              // Primary error message text
	File      string `json:"file,omitempty"`       // Source file path, e.g. "/src/App.tsx"
	Line      int    `json:"line,omitempty"`       // Line number in source
	Column    int    `json:"column,omitempty"`     // Column number in source
	Stack     string `json:"stack,omitempty"`      // Full stack trace or component stack
	CodeFrame string `json:"code_frame,omitempty"` // Code snippet frame showing error line
}

const detectDevOverlayJS = `(() => {
  try {
    // 1. Vite Error Overlay (<vite-error-overlay>)
    const viteOverlay = document.querySelector('vite-error-overlay');
    if (viteOverlay) {
      const root = viteOverlay.shadowRoot || viteOverlay;
      const plugin = root.querySelector('.plugin')?.textContent?.trim() || '';
      const msg = root.querySelector('.message-body')?.textContent?.trim() ||
                  root.querySelector('.message')?.textContent?.trim() || '';
      const file = root.querySelector('.file')?.textContent?.trim() || '';
      const frame = root.querySelector('.frame')?.textContent?.trim() || '';
      const stack = root.querySelector('.stack')?.textContent?.trim() || '';
      return {
        detected: true,
        framework: 'vite',
        title: plugin ? ('Vite Error [' + plugin + ']') : 'Vite Error Overlay',
        message: msg || 'Vite compilation or runtime error',
        file_raw: file,
        code_frame: frame,
        stack: stack || frame
      };
    }

    // 2. Next.js Error Overlay (<nextjs-portal>, [data-nextjs-dialog])
    let nextRoot = null;
    const nextPortal = document.querySelector('nextjs-portal') || document.getElementById('nextjs-portal');
    if (nextPortal && nextPortal.shadowRoot) {
      nextRoot = nextPortal.shadowRoot;
    } else {
      const allEls = document.querySelectorAll('*');
      for (let i = 0; i < allEls.length; i++) {
        const sr = allEls[i].shadowRoot;
        if (sr && sr.querySelector('[data-nextjs-dialog], [data-nextjs-terminal], .nextjs-container-build-error, .nextjs-container-errors')) {
          nextRoot = sr;
          break;
        }
      }
    }
    if (!nextRoot) {
      const el = document.querySelector('[data-nextjs-dialog], .nextjs-container-build-error, .nextjs-container-errors, #nextjs-container-build-error');
      if (el) nextRoot = document;
    }
    if (nextRoot) {
      const header = nextRoot.querySelector('[data-nextjs-dialog-header], h1, h2, h3, [data-nextjs-dialog-banner]')?.innerText?.trim() || 'Next.js Error';
      const body = nextRoot.querySelector('[data-nextjs-dialog-body], [data-nextjs-terminal], .nextjs-container-errors, pre')?.innerText?.trim() || '';
      const callStack = nextRoot.querySelector('[data-nextjs-call-stack], [data-nextjs-call-stack-frame]')?.innerText?.trim() || '';
      const codeframe = nextRoot.querySelector('[data-nextjs-codeframe], pre')?.innerText?.trim() || '';
      const file = nextRoot.querySelector('[data-nextjs-dialog-left-right], [data-nextjs-call-stack-frame]')?.innerText?.trim() || '';
      return {
        detected: true,
        framework: 'nextjs',
        title: header,
        message: body || header,
        file_raw: file,
        code_frame: codeframe,
        stack: callStack || body
      };
    }

    // 3. Webpack / Create React App Overlay
    const wpIframe = document.querySelector('iframe#webpack-dev-server-client-overlay, iframe[src*="webpack-dev-server"], webpack-dev-server-client-overlay');
    if (wpIframe) {
      let doc = null;
      try {
        doc = wpIframe.contentDocument || wpIframe.contentWindow?.document || wpIframe.shadowRoot;
      } catch (e) {}
      if (doc && doc.body) {
        const text = doc.body.innerText?.trim() || '';
        const header = doc.querySelector('h1, h2, div.header, [style*="color: rgb(230, 0, 0)"]')?.innerText?.trim() || 'Webpack Error';
        const stack = doc.querySelector('pre, code')?.innerText?.trim() || text;
        return {
          detected: true,
          framework: 'webpack',
          title: header,
          message: text.slice(0, 1000),
          file_raw: text,
          stack: stack
        };
      }
      return {
        detected: true,
        framework: 'webpack',
        title: 'Webpack Dev Server Overlay',
        message: 'Webpack error overlay is present',
        file_raw: '',
        stack: ''
      };
    }

    // 4. React Error Boundary / Generic Overlays
    const reactOverlay = document.querySelector('div[data-react-error-overlay="true"], [data-error-boundary="true"], div.remix-error-boundary, #__nuxt-error');
    if (reactOverlay) {
      const text = reactOverlay.innerText?.trim() || '';
      if (text.length > 0) {
        return {
          detected: true,
          framework: 'react_error_boundary',
          title: 'Application Error Boundary',
          message: text.slice(0, 800),
          file_raw: text,
          stack: text
        };
      }
    }

    return { detected: false };
  } catch (err) {
    return { detected: false, error: String(err) };
  }
})()`

var fileLocationRegex = regexp.MustCompile(`((?:[a-zA-Z0-9_\-\.\/]+?\.[a-zA-Z0-9]+))[:\s(]+([0-9]+)(?:[:,\s]+([0-9]+))?`)

// parseFileLocation extracts file path, line number, and column number from raw strings.
func parseFileLocation(raw string) (string, int, int) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, 0
	}
	matches := fileLocationRegex.FindStringSubmatch(raw)
	if len(matches) >= 3 {
		filePath := matches[1]
		if strings.HasPrefix(filePath, "///") {
			filePath = strings.TrimPrefix(filePath, "///")
		} else if strings.HasPrefix(filePath, "//") {
			filePath = strings.TrimPrefix(filePath, "//")
		}
		line, _ := strconv.Atoi(matches[2])
		col := 0
		if len(matches) >= 4 && matches[3] != "" {
			col, _ = strconv.Atoi(matches[3])
		}
		return filePath, line, col
	}
	return raw, 0, 0
}

// checkDevOverlay queries the target page for any active framework development error overlay.
func checkDevOverlay(ws string) (*DevOverlayError, error) {
	if ws == "" {
		return nil, fmt.Errorf("empty websocket url")
	}
	val, err := cdpEvaluate(ws, detectDevOverlayJS, true)
	if err != nil {
		return nil, err
	}
	data, ok := val.(map[string]any)
	if !ok || data == nil {
		return nil, nil
	}
	detected, _ := data["detected"].(bool)
	if !detected {
		return nil, nil
	}

	framework, _ := data["framework"].(string)
	title, _ := data["title"].(string)
	message, _ := data["message"].(string)
	fileRaw, _ := data["file_raw"].(string)
	codeFrame, _ := data["code_frame"].(string)
	stack, _ := data["stack"].(string)

	filePath, line, col := parseFileLocation(fileRaw)
	if filePath == "" && stack != "" {
		filePath, line, col = parseFileLocation(stack)
	}

	return &DevOverlayError{
		Detected:  true,
		Framework: framework,
		Title:     title,
		Message:   message,
		File:      filePath,
		Line:      line,
		Column:    col,
		Stack:     stack,
		CodeFrame: codeFrame,
	}, nil
}
