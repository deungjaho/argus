package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type getElementInput struct {
	Ref      string `json:"ref,omitempty"`
	Selector string `json:"selector,omitempty"`
	Include  string `json:"include,omitempty"` // html | attributes | style | bounding_box | all (default: all)
	Target   string `json:"target,omitempty"`
	CSS      string `json:"css,omitempty"`
	XPath    string `json:"xpath,omitempty"`
	Text     string `json:"text,omitempty"`
	Query    string `json:"query,omitempty"`
	Fields   string `json:"fields,omitempty"`
}

func registerElementTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "get_element",
		Description: "Get outerHTML, attributes, computed style, or bounding box of an element. include: html, attributes, style, bounding_box, all.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input getElementInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		selector := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, input.Query)
		text := resolveFirstNonEmpty(input.Text, input.Query)
		if selector == "" && text == "" {
			return nil, nil, fmt.Errorf("get_element requires ref, selector, or text")
		}
		include := resolveFirstNonEmpty(input.Include, input.Fields)
		if include == "" {
			include = "all"
		}
		value, err := cdpEvaluate(ws, getElementExpression(selector, text, include), true)
		return nil, value, err
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
