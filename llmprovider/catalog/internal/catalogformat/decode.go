// Package catalogformat decodes upstream legacy maps and compact catalog v1.
// The wire semantics follow oh-my-pi's model-catalog-format.ts. Original source
// bytes, not this normalized representation, remain the provenance artifact.
// Reference: can1357/oh-my-pi at 9319b0b3074e2198f5f20db153643109fd01cc29,
// packages/catalog/src/model-catalog-format.ts.
package catalogformat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
)

const MaxBytes = 32 << 20
const maxExpanded = 128 << 20
const maxDepth = 64

type undefined struct{}
type decoder struct {
	keys   []string
	shapes [][]int
	nodes  [][]any
	budget int
}

// Decode normalizes compact v1 into provider/model maps. Unknown top-level
// extension fields are ignored. Limits bound both source and expanded work.
func Decode(raw []byte) (out []byte, err error) {
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("catalog source exceeds %d bytes", MaxBytes)
	}
	var top map[string]json.RawMessage
	if err = json.Unmarshal(raw, &top); err != nil || top == nil {
		return nil, fmt.Errorf("invalid catalog object: %v", err)
	}
	if _, ok := top["version"]; !ok {
		return raw, nil
	}
	var version int
	if json.Unmarshal(top["version"], &version) != nil || version != 1 {
		return nil, fmt.Errorf("unsupported catalog version: %s", top["version"])
	}
	// Internal validation uses a private panic sentinel to keep all tuple/type
	// checks explicit without letting malformed input escape as a runtime panic.
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(formatError); ok {
				out = nil
				err = fmt.Errorf("compact catalog: %s", e)
			} else {
				panic(r)
			}
		}
	}()
	var wire struct {
		Keys          []string
		BooleanShapes [][]int
		Nodes         [][]any
		Compat        [][]any
		Providers     [][]any
	}
	jd := json.NewDecoder(bytes.NewReader(raw))
	jd.UseNumber()
	if err = jd.Decode(&wire); err != nil {
		return nil, err
	}
	if wire.Keys == nil || wire.BooleanShapes == nil || wire.Nodes == nil || wire.Compat == nil || wire.Providers == nil {
		fail("missing compact tables")
	}
	d := decoder{wire.Keys, wire.BooleanShapes, wire.Nodes, maxExpanded}
	unique := map[string]bool{}
	for _, k := range d.keys {
		if unique[k] {
			fail("duplicate key")
		}
		unique[k] = true
	}
	for _, shape := range d.shapes {
		seen := map[int]bool{}
		for _, k := range shape {
			d.key(k)
			if seen[k] {
				fail("duplicate boolean key")
			}
			seen[k] = true
		}
	}
	// Validate even unused nodes; per-node work counts toward the same budget.
	for i := range d.nodes {
		d.value([]any{json.Number(fmt.Sprint(i))}, 0)
	}
	bases := map[string]map[string]any{}
	for _, row := range wire.Compat {
		length(row, 2)
		api := text(row[0])
		if _, ok := bases[api]; ok {
			fail("duplicate compat base")
		}
		bases[api] = object(d.value(row[1], 0))
	}
	db := map[string]map[string]any{}
	for _, row := range wire.Providers {
		length(row, 3)
		p := text(row[0])
		if _, ok := db[p]; ok {
			fail("duplicate provider")
		}
		constants := object(d.value(row[1], 0))
		models := map[string]any{}
		db[p] = models
		for _, v := range array(row[2]) {
			mrow := array(v)
			if len(mrow) != 2 && len(mrow) != 3 {
				fail("invalid model row")
			}
			id := text(mrow[0])
			if _, ok := models[id]; ok {
				fail("duplicate model")
			}
			model := map[string]any{"id": id}
			maps.Copy(model, constants)
			maps.Copy(model, object(d.value(mrow[1], 0)))
			if len(mrow) == 3 {
				delta := array(mrow[2])
				length(delta, 2)
				base, ok := bases[text(model["api"])]
				if !ok {
					fail("missing compat base")
				}
				compat := maps.Clone(base)
				for _, k := range array(delta[0]) {
					delete(compat, d.key(integer(k)))
				}
				maps.Copy(compat, object(d.value(delta[1], 0)))
				model["compat"] = compat
			}
			// Charge every model's serialized expansion, including shared constants.
			clean(model)
			b, e := json.Marshal(model)
			if e != nil {
				fail("invalid expanded model")
			}
			d.charge(len(b))
			models[id] = json.RawMessage(b)
		}
	}
	out, err = json.Marshal(db)
	if len(out) > maxExpanded {
		return nil, fmt.Errorf("expanded catalog exceeds limit")
	}
	return
}

type formatError string

func fail(s string) { panic(formatError(s)) }
func length(a []any, n int) {
	if len(a) != n {
		fail("invalid tuple length")
	}
}
func array(v any) []any {
	a, ok := v.([]any)
	if !ok {
		fail("expected array")
	}
	return a
}
func object(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		fail("expected object reference")
	}
	return m
}
func text(v any) string {
	s, ok := v.(string)
	if !ok {
		fail("expected string")
	}
	return s
}
func integer(v any) int {
	n, ok := v.(json.Number)
	if !ok {
		fail("expected integer")
	}
	i, e := n.Int64()
	if e != nil || i > 1<<32 || i < -1<<31 {
		fail("invalid integer")
	}
	return int(i)
}
func (d *decoder) key(i int) string {
	if i < 0 || i >= len(d.keys) {
		fail("key index out of range")
	}
	return d.keys[i]
}
func (d *decoder) charge(n int) {
	d.budget -= n
	if d.budget < 0 {
		fail("expanded catalog work exceeds limit")
	}
}
func (d *decoder) value(v any, depth int) any {
	d.charge(16)
	if depth > maxDepth {
		fail("node depth exceeds limit (cycle or excessive nesting)")
	}
	ref, ok := v.([]any)
	if !ok {
		switch v.(type) {
		case nil, string, json.Number, bool:
			if s, ok := v.(string); ok {
				d.charge(len(s))
			}
			return v
		default:
			fail("invalid encoded value")
		}
	}
	length(ref, 1)
	i := integer(ref[0])
	if i == -1 {
		return undefined{}
	}
	if i < 0 || i >= len(d.nodes) {
		fail("node reference out of range")
	}
	node := d.nodes[i]
	if len(node) < 2 {
		fail("invalid node")
	}
	switch integer(node[0]) {
	case 1:
		length(node, 2)
		values := array(node[1])
		a := make([]any, len(values))
		for j, v := range values {
			a[j] = d.value(v, depth+1)
		}
		return a
	case 0:
		length(node, 4)
		pairs := array(node[1])
		if len(pairs)%2 != 0 {
			fail("odd object pairs")
		}
		m := map[string]any{}
		for j := 0; j < len(pairs); j += 2 {
			k := d.key(integer(pairs[j]))
			d.charge(len(k))
			if _, ok := m[k]; ok {
				fail("duplicate object key")
			}
			m[k] = d.value(pairs[j+1], depth+1)
		}
		si := integer(node[2])
		if si < 0 || si >= len(d.shapes) {
			fail("boolean shape out of range")
		}
		shape := d.shapes[si]
		masks := array(node[3])
		if len(masks) != (len(shape)+31)/32 {
			fail("invalid boolean masks")
		}
		for _, mask := range masks {
			n := integer(mask)
			if n > int(^uint32(0)) {
				fail("invalid boolean mask")
			}
		}
		for j, ki := range shape {
			k := d.key(ki)
			d.charge(len(k))
			if _, ok := m[k]; ok {
				fail("duplicate boolean property")
			}
			m[k] = (uint32(integer(masks[j/32]))>>uint(j%32))&1 != 0
		}
		return m
	default:
		fail("unknown node kind")
	}
	return nil
}

// Match JSON.stringify: undefined properties disappear; undefined array slots
// become null. Keep undefined until after overlays so it can erase a base field.
func clean(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if _, ok := v.(undefined); ok {
				delete(x, k)
			} else {
				clean(v)
			}
		}
	case []any:
		for i, v := range x {
			if _, ok := v.(undefined); ok {
				x[i] = nil
			} else {
				clean(v)
			}
		}
	}
}
