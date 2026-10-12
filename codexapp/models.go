package codexapp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mudler/nib/llmprovider/registry"
)

const maxModelPages = 20

func (l *LLM) ListModels(ctx context.Context) ([]string, error) {
	r := l.DiscoverModels(ctx)
	return r.IDs, r.Err
}

// DiscoverModels uses only initialize/initialized and model/list. Visibility
// does not establish account access. Protocol: openai/codex
// 16845bb8e85d781f6e745bdec011384450929ff9, schema/typescript/v2/ModelListParams.ts.
func (l *LLM) DiscoverModels(ctx context.Context) (result registry.ModelListResult) {
	defer func() {
		if ctx.Err() != nil {
			result.Err = ctx.Err()
		}
		result.Partial = result.Err != nil
	}()
	rpc, stderr, cleanup, err := l.startRPC(ctx)
	if err != nil {
		result.Err = err
		return
	}
	defer func() {
		cleanup()
		if result.Err != nil {
			result.Err = withStderr(result.Err, stderr)
		}
	}()
	seenIDs, seenCursors := map[string]bool{}, map[string]bool{}
	var cursor *string
	for range maxModelPages {
		id, err := rpc.send("model/list", map[string]any{"cursor": cursor, "limit": 100, "includeHidden": false})
		if err != nil {
			result.Err = err
			return
		}
		var page struct {
			Data []struct {
				ID     string `json:"id"`
				Hidden bool   `json:"hidden"`
			} `json:"data"`
			NextCursor json.RawMessage `json:"nextCursor"`
		}
		if err := rpc.response(id, &page); err != nil {
			result.Err = err
			return
		}
		for _, m := range page.Data {
			if m.ID != "" && !m.Hidden && !seenIDs[m.ID] {
				seenIDs[m.ID] = true
				result.IDs = append(result.IDs, m.ID)
			}
		}
		if len(page.NextCursor) == 0 {
			result.Err = fmt.Errorf("codex app-server: missing model continuation cursor")
			return
		}
		if err := json.Unmarshal(page.NextCursor, &cursor); err != nil {
			result.Err = err
			return
		}
		if cursor == nil {
			return
		}
		if *cursor == "" || seenCursors[*cursor] {
			result.Err = fmt.Errorf("codex app-server: empty or repeated model cursor")
			return
		}
		seenCursors[*cursor] = true
	}
	result.Err = fmt.Errorf("codex app-server: model page limit reached")
	return
}
