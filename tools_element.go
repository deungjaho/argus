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
}

func registerElementTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "get_element",
		Description: "Get detailed info about an element: outerHTML, attributes, computed style, bounding box. Useful for debugging layout/CSS issues. include options: html, attributes, style, bounding_box, all (default).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input getElementInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		selector := firstNonEmpty(strings.TrimSpace(input.Ref), strings.TrimSpace(input.Selector))
		if selector == "" {
			return nil, nil, fmt.Errorf("get_element requires ref or selector")
		}
		include := strings.TrimSpace(input.Include)
		if include == "" {
			include = "all"
		}
		value, err := cdpEvaluate(ws, getElementExpression(selector, include), true)
		return nil, value, err
	})
}

func getElementExpression(selector, include string) string {
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
const el = document.querySelector(selector);
if (!el) return {found: false};
const rect = el.getBoundingClientRect();
return Object.assign({found: true, %s});
})()`, jsString(selector), strings.Join(parts, ""))
}
