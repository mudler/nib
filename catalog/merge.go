package catalog

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// SourceError is a per-source failure from Merge. The rest of the sources still
// return, so a single unreachable or malformed source never aborts a browse.
type SourceError struct {
	Source string
	Err    error
}

// Error implements error.
func (e SourceError) Error() string {
	return fmt.Sprintf("source %q: %v", e.Source, e.Err)
}

// Merge fetches every enabled source concurrently, stamps each Meta with its
// source label and trust, unions the results, and dedupes by (Kind, Name,
// Source). A per-source failure is non-fatal: it is collected into errs while
// the remaining sources still contribute. metas are sorted by category then
// name for deterministic output regardless of fetch-completion order.
func (c *Client) Merge(ctx context.Context, sources []Source) (metas []Meta, errs []SourceError) {
	type result struct {
		metas []Meta
		err   *SourceError
	}
	results := make([]result, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		if !s.Enabled {
			continue
		}
		wg.Add(1)
		go func(i int, s Source) {
			defer wg.Done()
			ms, err := c.Fetch(ctx, s)
			if err != nil {
				results[i] = result{err: &SourceError{Source: s.Label, Err: err}}
				return
			}
			for j := range ms {
				ms[j].Source = s.Label
				ms[j].Trust = s.Trust
			}
			results[i] = result{metas: ms}
		}(i, s)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, r := range results { // fixed source order → stable dedupe
		if r.err != nil {
			errs = append(errs, *r.err)
			continue
		}
		for _, m := range r.metas {
			key := string(m.Kind) + "\x00" + m.Name + "\x00" + m.Source
			if seen[key] {
				continue
			}
			seen[key] = true
			metas = append(metas, m)
		}
	}
	sort.SliceStable(metas, func(i, j int) bool {
		if metas[i].Category != metas[j].Category {
			return metas[i].Category < metas[j].Category
		}
		return metas[i].Name < metas[j].Name
	})
	return metas, errs
}
