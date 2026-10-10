package main

import (
	"context"
	"fmt"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// diff_snapshot compares the current page against a stored snapshot and
// reports what changed: added/removed/recreated/changed elements, URL
// change, plus evidence (console errors, network failures, navigations)
// observed since the stored snapshot. This is how the agent answers
// "did my click do anything?" without re-reading a whole page.
type diffSnapshotInput struct {
	SnapshotID  string `json:"snapshot_id,omitempty"`
	Snapshot    string `json:"snapshot,omitempty"`
	ID          string `json:"id,omitempty"`
	Target      string `json:"target,omitempty"`
	TargetID    string `json:"target_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`
	MaxElements int    `json:"max_elements,omitempty"`
}

func registerDiffTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name: "diff_snapshot",
		Description: "Compare the current page against a stored snapshot. " +
			"Returns added/removed/recreated/changed elements, URL change, and evidence (console errors, failed requests, navigations) observed since that snapshot. " +
			"Use after an action to see what it actually did. Default compares against the most recent snapshot for this tab.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input diffSnapshotInput) (*mcp.CallToolResult, any, error) {
		ar := BeginAction("diff_snapshot")
		defer ar.End()
		ar.SetParams(input)

		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			ar.SetErrorFromError(err)
			return nil, nil, err
		}
		ar.SetTarget(getCurrentTargetID())

		var base *storedSnapshot
		if id := resolveFirstNonEmpty(input.SnapshotID, input.Snapshot, input.ID); id != "" {
			var ok bool
			base, ok = getSnapshot(id)
			if !ok {
				err := fmt.Errorf("snapshot %s not found (expired or from another run); call snapshot or look first", id)
				ar.SetErrorFromError(err)
				return nil, nil, err
			}
		} else {
			base = latestSnapshot(targetIDFromWSURL(ws))
			if base == nil {
				err := fmt.Errorf("no stored snapshot for this tab; call snapshot or look first")
				ar.SetErrorFromError(err)
				return nil, nil, err
			}
		}

		maxElements := input.MaxElements
		if maxElements <= 0 {
			maxElements = 200
		}
		if maxElements > 300 {
			maxElements = 300
		}
		value, err := cdpEvaluate(ws, snapshotExpression(0, maxElements), true)
		if err != nil {
			ar.SetErrorFromError(err)
			return nil, nil, err
		}
		info, _ := value.(map[string]any)
		curURL, _ := info["url"].(string)
		curEls, _ := info["interactive"].([]any)

		added, removed, recreated, changed := []map[string]any{}, []map[string]any{}, []map[string]any{}, []map[string]any{}
		unchanged := 0
		seen := map[string]bool{}
		for _, raw := range curEls {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			ref, _ := m["ref"].(string)
			if ref == "" {
				continue
			}
			seen[ref] = true
			cur := snapshotElement{Ref: ref}
			cur.Mark, _ = m["m"].(string)
			cur.Tag, _ = m["tag"].(string)
			cur.Text, _ = m["text"].(string)
			cur.Rect, _ = m["rect"].(map[string]any)
			before, ok := base.Elements[ref]
			if !ok {
				added = append(added, map[string]any{"ref": ref, "tag": cur.Tag, "text": cur.Text})
				continue
			}
			if cur.Mark != before.Mark {
				recreated = append(recreated, map[string]any{"ref": ref, "tag": cur.Tag, "text": cur.Text})
				continue
			}
			if cur.Text != before.Text || !reflect.DeepEqual(cur.Rect, before.Rect) {
				changed = append(changed, map[string]any{
					"ref":    ref,
					"tag":    cur.Tag,
					"before": map[string]any{"text": before.Text, "rect": before.Rect},
					"after":  map[string]any{"text": cur.Text, "rect": cur.Rect},
				})
				continue
			}
			unchanged++
		}
		for _, before := range base.Elements {
			if !seen[before.Ref] {
				removed = append(removed, map[string]any{"ref": before.Ref, "tag": before.Tag, "text": before.Text})
			}
		}

		// Store the fresh snapshot too so its refs/marks are usable.
		fresh := storeSnapshot(ws, curURL, curEls)
		res := map[string]any{
			"action_id":       string(ar.ID),
			"snapshot_id":     fresh.ID,
			"compared_to":     base.ID,
			"compared_at":     base.At.Format("2006-01-02T15:04:05.000Z07:00"),
			"added":           added,
			"removed":         removed,
			"recreated":       recreated,
			"changed":         changed,
			"unchanged_count": unchanged,
		}
		if curURL != base.URL {
			res["url_changed"] = map[string]any{"from": base.URL, "to": curURL}
		}
		if ev := rec.EvidenceSince(base.TargetID, base.At); ev != nil {
			res["evidence"] = ev
		}
		ar.SetResult(res)
		return nil, res, nil
	})
}
