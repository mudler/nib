package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// indexDoc is the on-the-wire agentskills.io index document: a top-level object
// carrying entries under "skills" (the historical name) and/or "extensions".
type indexDoc struct {
	Skills     []Meta `json:"skills"`
	Extensions []Meta `json:"extensions"`
}

// ParseIndex parses an agentskills.io index document into metas. It accepts both
// the object form ({"skills":[…]} and/or {"extensions":[…]}) and a bare
// top-level array. Each entry's Kind defaults to KindSkill when absent, matching
// the agentskills.io convention that an index without a kind field lists skills.
func ParseIndex(data []byte) ([]Meta, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []Meta
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, fmt.Errorf("parse index array: %w", err)
		}
		return normalizeKinds(arr), nil
	}
	var doc indexDoc
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("parse index object: %w", err)
	}
	return normalizeKinds(append(doc.Skills, doc.Extensions...)), nil
}

// normalizeKinds defaults every entry's Kind to KindSkill when the index omitted
// it. It returns metas unchanged otherwise (nil in, nil out).
func normalizeKinds(metas []Meta) []Meta {
	for i := range metas {
		if metas[i].Kind == "" {
			metas[i].Kind = KindSkill
		}
	}
	return metas
}
