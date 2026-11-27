package catalogformat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const fixture = `{"version":1,"keys":["provider","api","input","reasoning","old","keep","added","missing"],"booleanShapes":[[],[3]],"nodes":[[0,[0,"p",1,"a"],0,[]],[1,["text",null,[-1]]],[0,[2,[1],7,[-1]],1,[1]],[0,[4,1,5,2],0,[]],[0,[6,3],0,[]]],"compat":[["a",[3]]],"providers":[["p",[0],[["m",[2],[[4],[4]]],["n",[2],[[],[4]]]]]]}`

func TestDecodeFormats(t *testing.T) {
	b, err := Decode([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]map[string]any
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	m := got["p"]["m"]
	if m["id"] != "m" || m["provider"] != "p" || m["reasoning"] != true {
		t.Fatal(m)
	}
	if _, ok := m["missing"]; ok {
		t.Fatal("undefined property retained")
	}
	c := m["compat"].(map[string]any)
	if c["added"] != float64(3) || c["keep"] != float64(2) || c["old"] != nil {
		t.Fatal(c)
	}
	if got["p"]["n"]["compat"].(map[string]any)["old"] != float64(1) {
		t.Fatal("base mutated")
	}
	a := m["input"].([]any)
	if len(a) != 3 || a[1] != nil || a[2] != nil {
		t.Fatal(a)
	}
	legacy := `{"p":{"m":{"id":"m","provider":"p"}}}`
	if b, e := Decode([]byte(legacy)); e != nil || string(b) != legacy {
		t.Fatalf("%s %v", b, e)
	}
}
func TestMalformed(t *testing.T) {
	for _, s := range []string{
		strings.Replace(fixture, `"version":1`, `"version":2`, 1),
		strings.Replace(fixture, `[2,[1],7`, `[2,[999],7`, 1),
		strings.Replace(fixture, `[1,["text",null,[-1]]]`, `[1,[[1]]]`, 1),
		strings.Replace(fixture, `1,[1]]`, `1,[]]`, 1),
		strings.Replace(fixture, `[0,[0,"p",1,"a"]`, `[0,[999,"p",1,"a"]`, 1),
		strings.Replace(fixture, `["a",[3]]`, `["other",[3]]`, 1),
		strings.Replace(fixture, `[2,[1],7`, `[2,[1,2],7`, 1),
		strings.Replace(fixture, `[2,[1],7`, `[2,{},7`, 1),
		strings.Replace(fixture, `[0,[4,1,5,2]`, `[9,[4,1,5,2]`, 1),
		strings.Replace(fixture, `[0,[4,1,5,2]`, `[0,[4,1,5]`, 1),
		strings.Replace(fixture, `1,[1]]`, `999,[1]]`, 1),
		strings.Replace(fixture, `1,[1]]`, `1,[4294967296]]`, 1),
		strings.Replace(fixture, `["m",[2]`, `["m",[1]`, 1),
		strings.Replace(fixture, `["m",[2]`, `["m",[-1]`, 1),
		strings.Replace(fixture, `["n",[2]`, `["m",[2]`, 1),
		strings.Replace(fixture, `[[4],[4]]`, `[[999],[4]]`, 1),
		`{"version":1}`, `null`,
	} {
		if _, e := Decode([]byte(s)); e == nil {
			t.Errorf("accepted %s", s)
		}
	}
}

func TestMasksOverridesAndSpecialKeys(t *testing.T) {
	keys := []string{"provider", "api", "__proto__", "id"}
	shape := []int{}
	for i := 0; i < 33; i++ {
		keys = append(keys, fmt.Sprint("b", i))
		shape = append(shape, i+4)
	}
	wire := map[string]any{"version": 1, "keys": keys, "booleanShapes": [][]int{{}, shape}, "nodes": []any{
		[]any{0, []any{0, "p", 1, "a", 3, "constant-id"}, 0, []any{}},
		[]any{0, []any{2, "safe", 3, "row-id"}, 1, []any{-2147483648, 1}},
	}, "compat": []any{}, "providers": []any{[]any{"p", []int{0}, []any{[]any{"m", []int{1}}}}}}
	raw, _ := json.Marshal(wire)
	b, e := Decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	var db map[string]map[string]map[string]any
	json.Unmarshal(b, &db)
	m := db["p"]["m"]
	if m["id"] != "row-id" || m["__proto__"] != "safe" || m["b0"] != false || m["b31"] != true || m["b32"] != true {
		t.Fatal(m)
	}
}

func TestBounds(t *testing.T) {
	if _, e := Decode(make([]byte, MaxBytes+1)); e == nil {
		t.Fatal("oversize source accepted")
	}
	// Repeated references form a small input with exponential expansion.
	nodes := []any{[]any{1, []any{"leaf"}}}
	for i := 1; i < 24; i++ {
		nodes = append(nodes, []any{1, []any{[]int{i - 1}, []int{i - 1}}})
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "keys": []string{}, "booleanShapes": [][]int{{}}, "nodes": nodes, "compat": []any{}, "providers": []any{}})
	if _, e := Decode(raw); e == nil || !strings.Contains(e.Error(), "limit") {
		t.Fatalf("expansion: %v", e)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(fixture))
	f.Add([]byte(`{"p":{"m":{"id":"m"}}}`))
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}
