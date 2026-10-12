package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// getElementInput represents parameters for the unified canonical element inspection tool.
type getElementInput struct {
	Ref              string   `json:"ref,omitempty"`      // CSS ref or a11y nodeId (e.g. "12", "ax:12")
	Selector         string   `json:"selector,omitempty"` // CSS selector or id
	Text             string   `json:"text,omitempty"`     // Exact/fuzzy text search (absorbs find)
	Query            string   `json:"query,omitempty"`    // Query string alias
	Regex            string   `json:"regex,omitempty"`    // Regular expression string (absorbs find)
	Pattern          string   `json:"pattern,omitempty"`  // alias for regex/text
	X                *float64 `json:"x,omitempty"`        // Viewport X coordinate (absorbs inspect_point)
	Y                *float64 `json:"y,omitempty"`        // Viewport Y coordinate (absorbs inspect_point)
	ImageX           *float64 `json:"image_x,omitempty"`  // Alias for X
	ImageY           *float64 `json:"image_y,omitempty"`  // Alias for Y
	ImageWidth       *float64 `json:"image_width,omitempty"`
	ImageHeight      *float64 `json:"image_height,omitempty"`
	ScreenshotPath   string   `json:"screenshot_path,omitempty"`
	Include          string   `json:"include,omitempty"` // html | attributes | style | box | bounding_box | all (default: all)
	NodeID           string   `json:"node_id,omitempty"` // a11y node ID
	BackendNodeID    int      `json:"backend_node_id,omitempty"`
	BackendDOMNodeID int      `json:"backend_dom_node_id,omitempty"`
	Target           string   `json:"target,omitempty"`
	TargetID         string   `json:"target_id,omitempty"`
	TabID            string   `json:"tab_id,omitempty"`
	CSS              string   `json:"css,omitempty"`
	XPath            string   `json:"xpath,omitempty"`
	Fields           string   `json:"fields,omitempty"`
}

func registerElementTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "get_element",
		Description: "Inspect an element by ref/selector, coordinate (x, y), or text query. Returns outerHTML, attributes, computed style, bounding box, and ranked matches.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input getElementInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}

		include := resolveFirstNonEmpty(input.Include, input.Fields)
		if include == "" {
			include = "all"
		}

		// 1. Coordinate Inspection (Inspect Point Mode)
		if input.X != nil || input.ImageX != nil {
			x, y, err := resolveInspectPoint(ws, input)
			if err == nil {
				val, err := cdpEvaluate(ws, inspectPointExpression(x, y), true)
				return nil, val, err
			}
		}

		// 2. Selector / Ref / a11y NodeID Inspection
		selector := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, "")
		if selector == "" {
			if input.NodeID != "" {
				selector = "ax:" + input.NodeID
			} else if input.BackendNodeID > 0 {
				selector = fmt.Sprintf("backend:%d", input.BackendNodeID)
			} else if input.BackendDOMNodeID > 0 {
				selector = fmt.Sprintf("backend:%d", input.BackendDOMNodeID)
			}
		}

		if selector != "" {
			if a11yNode, isA11y, err := resolveA11yNode(ws, selector); err != nil {
				return nil, nil, err
			} else if isA11y {
				objectID, err := cdpResolveNode(ws, a11yNode.BackendDOMNodeID)
				if err != nil {
					return nil, nil, err
				}
				defer cdpRequest(ws, "Runtime.releaseObject", map[string]any{"objectId": objectID}, nil)
				value, err := cdpCallFunctionOn(ws, objectID, a11yGetElementFunction, []any{include}, true)
				if err != nil {
					return nil, nil, err
				}
				if resMap, ok := value.(map[string]any); ok {
					resMap["backend_dom_node_id"] = a11yNode.BackendDOMNodeID
					resMap["ax_node_id"] = a11yNode.NodeID
					if a11yNode.Role != "" {
						resMap["role"] = a11yNode.Role
					}
					if a11yNode.Name != "" {
						resMap["name"] = a11yNode.Name
					}
					return nil, resMap, nil
				}
				return nil, value, nil
			}
			value, err := cdpEvaluate(ws, getElementExpression(selector, "", include), true)
			return nil, value, err
		}

		// 3. Text / Query / Regex Search Mode (Absorbs Find)
		searchText := resolveFirstNonEmpty(input.Text, input.Query, input.Pattern)
		regexPattern := strings.TrimSpace(input.Regex)
		if searchText == "" && regexPattern == "" {
			return nil, nil, fmt.Errorf("get_element requires ref, selector, text, query, regex, or (x, y) coordinates")
		}

		value, err := cdpEvaluate(ws, findExpression(searchText, regexPattern), true)
		if err != nil {
			return nil, nil, err
		}

		if info, ok := value.(map[string]any); ok && info != nil {
			if rawMatches, ok := info["matches"].([]any); ok {
				for _, raw := range rawMatches {
					if mm, ok := raw.(map[string]any); ok {
						if t, ok := mm["text"].(string); ok {
							runes := []rune(t)
							if len(runes) > 100 {
								mm["text"] = string(runes[:100])
							}
						}
					}
				}
				if len(rawMatches) > 5 {
					info["matches"] = rawMatches[:5]
					info["count"] = 5
				}
			}
			total, _ := numberValue(info["total_count"])
			shown, _ := numberValue(info["count"])
			if total > 0 || shown > 0 {
				attachTruncation(info, newTruncationMeta(int(total), int(shown), "max_matches"))
			}
			if amb, ok := ambiguityFromMap(info); ok {
				info["ambiguity"] = amb
			}
		}
		return nil, value, nil
	})
}

func getElementExpression(selector, text, include string) string {
	includeHTML := include == "html" || include == "all"
	includeAttrs := include == "attributes" || include == "all"
	includeStyle := include == "style" || include == "all"
	includeBox := include == "bounding_box" || include == "all"
	parts := []string{`tag: el.localName,`}
	if includeHTML {
		parts = append(parts, `html: el.outerHTML.slice(0, 5000),`)
	}
	if includeAttrs {
		parts = append(parts, `attributes: (() => { const attrs = {}; for (const attr of el.attributes) attrs[attr.name] = attr.value; return attrs; })(),`)
	}
	if includeStyle {
		parts = append(parts, `computedStyle: (() => { const s = getComputedStyle(el); const picked = {}; const props = ['display','visibility','position','width','height','margin','padding','border','color','background-color','font-size','font-family','z-index','overflow','cursor','opacity','flex-direction','justify-content','align-items','gap']; for (const p of props) picked[p] = s.getPropertyValue(p); return picked; })(),`)
	}
	if includeBox {
		parts = append(parts, `boundingBox: {x: rect.x, y: rect.y, width: rect.width, height: rect.height, top: rect.top, right: rect.right, bottom: rect.bottom, left: rect.left},`)
	}
	return fmt.Sprintf(`(() => {
const selector = %s;
const text = %s;
let el = null;
if (selector) {
  try {
    el = document.querySelector(selector);
  } catch (e) {
    el = document.getElementById(selector) ||
         document.querySelector('[data-testid=' + JSON.stringify(selector) + ']') ||
         document.querySelector('[data-test-id=' + JSON.stringify(selector) + ']') ||
         document.querySelector('[aria-label=' + JSON.stringify(selector) + ']');
  }
}
`+rankedTextMatchJS+`
if (!el) return {found: false};
const rect = el.getBoundingClientRect();
return Object.assign({found: true, %s});
})()`, jsString(selector), jsString(text), strings.Join(parts, ""))
}
