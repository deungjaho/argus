package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// storageInput represents parameters for the unified canonical storage management tool.
type storageInput struct {
	Action      string `json:"action,omitempty"`       // "get" | "set" | "clear" (default: "get")
	Type        string `json:"type,omitempty"`         // "cookie" | "local" | "session" | "all"
	StorageType string `json:"storage_type,omitempty"` // alias for type
	Target      string `json:"target,omitempty"`       // alias for type
	Key         string `json:"key,omitempty"`          // cookie name or storage key
	Name        string `json:"name,omitempty"`         // alias for key
	Value       string `json:"value,omitempty"`        // value to write (for set)
	Domain      string `json:"domain,omitempty"`       // cookie only
	Path        string `json:"path,omitempty"`         // cookie only (default: /)
	MaxAge      int    `json:"max_age,omitempty"`      // cookie only (seconds)
}

// Backward-compatibility struct aliases
type getStorageInput = storageInput
type setStorageInput = storageInput
type clearStorageInput = storageInput

func registerStorageTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "storage",
		Description: "Browser storage CRUD: action can be 'get', 'set', or 'clear'. Type can be 'cookie', 'local', 'session', or 'all' (for clear). For set: specify key and value.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input storageInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}

		action := resolveStorageAction(input.Action, input.Value)

		storageType := resolveStorageType(resolveFirstNonEmpty(input.Type, input.StorageType, input.Target))
		key := resolveFirstNonEmpty(input.Key, input.Name)

		switch action {
		case "get":
			switch storageType {
			case "cookie":
				return getCookieStorage(ws, key)
			case "local":
				return getJSStorage(ws, "localStorage", key)
			case "session":
				return getJSStorage(ws, "sessionStorage", key)
			default:
				return nil, nil, fmt.Errorf("storage get type must be 'cookie', 'local', or 'session'")
			}

		case "set":
			rec := BeginAction("storage:set")
			defer rec.End()
			rec.SetTarget(getCurrentTargetID())
			rec.SetParams(input)

			if key == "" {
				err := fmt.Errorf("storage set requires key")
				rec.SetErrorFromError(err)
				return nil, nil, err
			}

			switch storageType {
			case "cookie":
				input.Key = key
				rr, res, err := setCookieStorage(ws, input, rec.ID)
				if err != nil {
					rec.SetErrorFromError(err)
				} else {
					rec.SetResult(res)
				}
				return rr, res, err
			case "local":
				rr, res, err := setJSStorage(ws, "localStorage", key, input.Value, rec.ID)
				if err != nil {
					rec.SetErrorFromError(err)
				} else {
					rec.SetResult(res)
				}
				return rr, res, err
			case "session":
				rr, res, err := setJSStorage(ws, "sessionStorage", key, input.Value, rec.ID)
				if err != nil {
					rec.SetErrorFromError(err)
				} else {
					rec.SetResult(res)
				}
				return rr, res, err
			default:
				err := fmt.Errorf("storage set type must be 'cookie', 'local', or 'session'")
				rec.SetErrorFromError(err)
				return nil, nil, err
			}

		case "clear":
			rec := BeginAction("storage:clear")
			defer rec.End()
			rec.SetTarget(getCurrentTargetID())
			rec.SetParams(input)

			if storageType == "" {
				storageType = "all"
			}
			results := map[string]any{}
			switch storageType {
			case "all":
				r1, _ := clearCookieStorage(ws)
				r2, _ := clearJSStorage(ws, "localStorage")
				r3, _ := clearJSStorage(ws, "sessionStorage")
				results["cookie"] = r1
				results["local"] = r2
				results["session"] = r3
			case "cookie":
				r, err := clearCookieStorage(ws)
				results["cookie"] = r
				if err != nil {
					results["action_id"] = string(rec.ID)
					rec.SetErrorFromError(err)
					rec.SetResult(results)
					return nil, results, err
				}
			case "local":
				r, err := clearJSStorage(ws, "localStorage")
				results["local"] = r
				if err != nil {
					results["action_id"] = string(rec.ID)
					rec.SetErrorFromError(err)
					rec.SetResult(results)
					return nil, results, err
				}
			case "session":
				r, err := clearJSStorage(ws, "sessionStorage")
				results["session"] = r
				if err != nil {
					results["action_id"] = string(rec.ID)
					rec.SetErrorFromError(err)
					rec.SetResult(results)
					return nil, results, err
				}
			default:
				err := fmt.Errorf("storage clear type must be 'all', 'cookie', 'local', or 'session'")
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			results["action_id"] = string(rec.ID)
			rec.SetResult(results)
			return nil, results, nil

		default:
			return nil, nil, fmt.Errorf("unknown storage action %q: must be 'get', 'set', or 'clear'", input.Action)
		}
	})
}

// resolveStorageAction normalizes the storage action or infers it based on value presence.
func resolveStorageAction(action, value string) string {
	act := strings.ToLower(strings.TrimSpace(action))
	if act == "" {
		if value != "" {
			return "set"
		}
		return "get"
	}
	return act
}

// --- cookie implementation via CDP ---

func getCookieStorage(ws, key string) (*mcp.CallToolResult, any, error) {
	var result struct {
		Cookies []map[string]any `json:"cookies"`
	}
	if err := cdpRequest(ws, "Network.getCookies", map[string]any{}, &result); err != nil {
		return nil, nil, err
	}
	// Redact sensitive cookie values to prevent token leakage in MCP output.
	// Values longer than 20 chars are likely session tokens/JWTs — show
	// only first 8 chars + length, not the full value.
	for i := range result.Cookies {
		if val, ok := result.Cookies[i]["value"].(string); ok && len(val) > 20 {
			result.Cookies[i]["value"] = val[:8] + "...(redacted, " + fmt.Sprintf("%d", len(val)) + " chars)"
			result.Cookies[i]["value_redacted"] = true
		}
	}
	if key != "" {
		for _, cookie := range result.Cookies {
			if name, _ := cookie["name"].(string); name == key {
				return nil, map[string]any{"key": key, "value": cookie["value"], "cookie": cookie}, nil
			}
		}
		return nil, map[string]any{"key": key, "value": nil, "found": false}, nil
	}
	return nil, map[string]any{"cookies": result.Cookies, "count": len(result.Cookies)}, nil
}

func setCookieStorage(ws string, input setStorageInput, actionID ActionID) (*mcp.CallToolResult, any, error) {
	// Get current URL info for default domain
	value, err := cdpEvaluate(ws, `(() => ({hostname: location.hostname, pathname: location.pathname}))()`, true)
	if err != nil {
		return nil, nil, err
	}
	info, ok := value.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("unable to read page location")
	}
	hostname, _ := info["hostname"].(string)
	pathname, _ := info["pathname"].(string)
	domain := input.Domain
	if domain == "" {
		domain = hostname
	}
	path := input.Path
	if path == "" {
		path = pathname
		if path == "" {
			path = "/"
		}
	}
	params := map[string]any{
		"name":   input.Key,
		"value":  input.Value,
		"domain": "." + strings.TrimPrefix(domain, "."),
		"path":   path,
	}
	if input.MaxAge > 0 {
		params["expires"] = float64(time.Now().Unix()) + float64(input.MaxAge)
	}
	if err := cdpRequest(ws, "Network.setCookie", params, nil); err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"set": true, "key": input.Key, "domain": domain, "path": path, "action_id": string(actionID)}, nil
}

func clearCookieStorage(ws string) (map[string]any, error) {
	// Get all cookies then delete each
	var result struct {
		Cookies []struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
			Path   string `json:"path"`
		} `json:"cookies"`
	}
	if err := cdpRequest(ws, "Network.getCookies", map[string]any{}, &result); err != nil {
		return nil, err
	}
	deleted := 0
	for _, cookie := range result.Cookies {
		if err := cdpRequest(ws, "Network.deleteCookies", map[string]any{
			"name":   cookie.Name,
			"domain": cookie.Domain,
			"path":   cookie.Path,
		}, nil); err == nil {
			deleted++
		}
	}
	return map[string]any{"cleared": deleted, "total": len(result.Cookies)}, nil
}

// --- localStorage / sessionStorage via JS ---

func getJSStorage(ws, storageType, key string) (*mcp.CallToolResult, any, error) {
	if key != "" {
		value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => { const v = %s.getItem(%s); return {key: %s, value: v, found: v !== null}; })()`, storageType, jsString(key), jsString(key)), true)
		return nil, value, err
	}
	value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => {
const entries = [];
for (let i = 0; i < %s.length; i++) {
  const key = %s.key(i);
  entries.push({key, value: %s.getItem(key)});
}
return {entries, count: entries.length};
})()`, storageType, storageType, storageType), true)
	return nil, value, err
}

func setJSStorage(ws, storageType, key, value string, actionID ActionID) (*mcp.CallToolResult, any, error) {
	_, err := cdpEvaluate(ws, fmt.Sprintf(`(() => { %s.setItem(%s, %s); return {set: true, key: %s}; })()`, storageType, jsString(key), jsString(value), jsString(key)), true)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"set": true, "key": key, "type": storageType, "action_id": string(actionID)}, nil
}

func clearJSStorage(ws, storageType string) (map[string]any, error) {
	value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => { const count = %s.length; %s.clear(); return {cleared: count}; })()`, storageType, storageType), true)
	if err != nil {
		return nil, err
	}
	if info, ok := value.(map[string]any); ok {
		return info, nil
	}
	return map[string]any{"cleared": -1}, nil
}
