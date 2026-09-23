package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mudler/nib/types"

	"gopkg.in/yaml.v3"
)

// SettingType is the value kind of a settable config key.
type SettingType string

const (
	SettingBool   SettingType = "bool"
	SettingInt    SettingType = "int"
	SettingFloat  SettingType = "float"
	SettingString SettingType = "string"
)

// Setting is one scalar key of the config file that /settings can read and
// write, addressed by its dotted yaml path ("compaction.threshold").
//
// The set is REFLECTED from types.Config's yaml tags rather than listed by
// hand, so a scalar field added to the config later is settable, listed and
// completed the moment it exists, with no second list to forget. Only the
// descriptions and the value hints live in hand-written tables below, and a key
// missing from those still works, it just shows its type instead of a sentence.
type Setting struct {
	Key  string
	Type SettingType
	// Doc is a one-line description, empty for keys with no entry in
	// settingDocs.
	Doc string
	// Values are the values offered for completion: on/off for every bool, the
	// modes for an enum-like string. Empty for free-form keys.
	Values []string
	// strict marks Values as exhaustive, so Parse rejects anything else. Hint
	// lists that a backend may legitimately extend (reasoning_effort) are not
	// strict: refusing a value nib merely has not heard of would be worse than
	// offering the common ones.
	strict bool
	// index is the reflect field path from types.Config to the leaf.
	index []int
}

// settingDocs describes the keys a user is most likely to reach for. Anything
// absent still lists, with its type standing in for the description.
var settingDocs = map[string]string{
	"ui.hide_hud":                             "hide the footer clock, cpu and memory badges",
	"ui.no_bell":                              "do not ring the terminal bell when nib needs you",
	"approval_mode":                           "tool-call gating: prompt, strict, allowlist, classify or auto",
	"classifier.endpoint":                     "named endpoint that serves the classifier",
	"classifier.model":                        "classifier model (e.g. a GLiNER SystemOne model)",
	"auto_approve.threshold":                  "min classifier confidence to auto-approve (0 = 0.85)",
	"suggestions.disabled":                    "turn off reply suggestions",
	"suggestions.threshold":                   "min confidence to show a suggestion (0 = 0.5)",
	"model":                                   "the model new sessions start on",
	"provider":                                "the main LLM transport (openai, codex, ...)",
	"base_url":                                "the OpenAI-compatible endpoint",
	"log_level":                               "log verbosity (debug, info, warn, error)",
	"reasoning_effort":                        "reasoning_effort sent on every request",
	"transcribe_model":                        "model for audio attachments (empty = auto)",
	"vision_model":                            "model for image attachments (empty = auto)",
	"video_model":                             "model for video attachments (empty = auto)",
	"session_retention":                       "recorded sessions kept for /resume (0 = 200)",
	"agent_options.iterations":                "tool-loop iterations per turn",
	"agent_options.max_attempts":              "attempts per tool call",
	"agent_options.max_retries":               "retries on a failed LLM call",
	"agent_options.force_reasoning":           "force a reasoning step before tool selection",
	"compaction.disabled":                     "turn off automatic compaction",
	"compaction.max_context_tokens":           "context window override (0 = auto-detect)",
	"compaction.threshold":                    "fraction of the budget at which compaction fires",
	"compaction.keep_recent":                  "messages kept verbatim when compacting",
	"compaction.reserve_tokens":               "tokens held back for the reply",
	"tool_output_pruning.disabled":            "turn off tool-output pruning",
	"tool_output_pruning.disable_stale_reads": "keep reads of files that were edited later",
	"tool_output_pruning.high_water_tokens":   "tool-output size at which pruning starts",
	"tool_output_pruning.low_water_tokens":    "tool-output size pruning shrinks to",
	"tool_output_pruning.min_result_tokens":   "results smaller than this are never pruned",
	"prompt_injection_protection.enabled":     "track and screen untrusted external data",
	"browser.enabled":                         "enable the browser automation tools",
	"browser.allow_private_urls":              "let the browser reach localhost and private networks",
}

// settingValues are the value hints for enum-like strings. The approval modes
// are exhaustive (chat.Session only knows these); the others are the common
// values of an open set.
var settingValues = map[string]struct {
	values []string
	strict bool
}{
	"approval_mode":    {[]string{"prompt", "strict", "allowlist", "classify", "auto"}, true},
	"reasoning_effort": {[]string{"none", "low", "medium", "high"}, false},
	"log_level":        {[]string{"debug", "info", "warn", "error"}, false},
	"provider":         {[]string{"openai", "codex"}, false},
}

// settingRanges bounds numeric keys beyond the blanket "no negatives" rule.
var settingRanges = map[string]struct{ lo, hi float64 }{
	// Threshold is a fraction of the budget. 0 is "unset" on the way in, so an
	// explicit 0 would silently become 0.8; `/settings compaction.threshold
	// default` is the way to say that.
	"compaction.threshold": {math.SmallestNonzeroFloat64, 1},
}

// blockDefaulted names the blocks withDefaults fills as a WHOLE when absent,
// rather than field by field. Writing one key into such a block where the file
// has none would make the block present and zero every sibling (see
// types.ToolOutputPruningConfig: a lone key turns size pruning OFF). So the
// first key written into an absent block seeds it with the defaults it was
// standing in for, and the user's change lands on top of those.
var blockDefaulted = []string{"tool_output_pruning"}

// secretLeaves are key names whose values are credentials. They are neither
// listed nor settable: /settings echoes its input into the transcript and the
// input history, so accepting one would leak it twice. /login and the file
// itself are the places for those.
//
// Matched on the whole leaf or its last underscore word, not by substring:
// "token" is a substring of every *_tokens budget (high_water_tokens,
// reserve_tokens), which are counts, not credentials.
var secretLeaves = []string{"key", "token", "password", "secret"}

func isSecretKey(key string) bool {
	leaf := key
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		leaf = key[i+1:]
	}
	leaf = strings.ToLower(leaf)
	if i := strings.LastIndexByte(leaf, '_'); i >= 0 {
		leaf = leaf[i+1:]
	}
	return containsString(secretLeaves, leaf)
}

var (
	settingsOnce sync.Once
	settingsList []Setting
)

// Settings returns every settable key, sorted by key.
func Settings() []Setting {
	settingsOnce.Do(func() {
		reflectSettings(reflect.TypeFor[types.Config](), "", nil, &settingsList)
		sort.Slice(settingsList, func(i, j int) bool { return settingsList[i].Key < settingsList[j].Key })
	})
	out := make([]Setting, len(settingsList))
	copy(out, settingsList)
	return out
}

// reflectSettings walks t's yaml-tagged fields. Scalars become settings,
// structs are recursed into, and everything else (slices, maps, pointers) is
// left to the file: a list of MCP servers or hooks has no sensible one-line
// form. Secrets are dropped here so no caller can list them by accident.
func reflectSettings(t reflect.Type, prefix string, index []int, out *[]Setting) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" || name == "" {
			// Untagged fields (ComputerConfig's) are not in the file format.
			continue
		}
		key := prefix + name
		idx := append(append([]int(nil), index...), i)
		var typ SettingType
		switch f.Type.Kind() {
		case reflect.Struct:
			reflectSettings(f.Type, key+".", idx, out)
			continue
		case reflect.Bool:
			typ = SettingBool
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			typ = SettingInt
		case reflect.Float32, reflect.Float64:
			typ = SettingFloat
		case reflect.String:
			typ = SettingString
		default:
			continue
		}
		if isSecretKey(key) {
			continue
		}
		s := Setting{Key: key, Type: typ, Doc: settingDocs[key], index: idx}
		if typ == SettingBool {
			s.Values, s.strict = []string{"on", "off"}, true
		} else if v, ok := settingValues[key]; ok {
			s.Values, s.strict = v.values, v.strict
		}
		*out = append(*out, s)
	}
}

// LookupSetting finds a settable key. An unknown key gets the closest real
// one suggested, and a secret gets told where it belongs instead.
func LookupSetting(key string) (Setting, error) {
	key = strings.TrimSpace(key)
	for _, s := range Settings() {
		if s.Key == key {
			return s, nil
		}
	}
	if isSecretKey(key) {
		return Setting{}, fmt.Errorf("%s holds a secret and is not settable here: use /login, or edit the config file", key)
	}
	if near := closestSetting(key); near != "" {
		return Setting{}, fmt.Errorf("unknown setting %q, did you mean %s?", key, near)
	}
	return Setting{}, fmt.Errorf("unknown setting %q · /settings lists them", key)
}

// closestSetting returns the key the user most plausibly meant: one whose last
// segment matches exactly ("hide_hud" for "ui.hide_hud"), else the nearest by
// edit distance within a tolerance that scales with the key's length.
func closestSetting(key string) string {
	best, bestD := "", math.MaxInt
	for _, s := range Settings() {
		if strings.HasSuffix(s.Key, "."+key) {
			return s.Key
		}
		if d := editDistance(key, s.Key); d < bestD {
			best, bestD = s.Key, d
		}
	}
	if bestD <= max(2, len(key)/3) {
		return best
	}
	return ""
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// Parse validates raw against the key's type and returns the typed value:
// bool, int, float64 or string. Bools take on/off, true/false and yes/no.
func (s Setting) Parse(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	switch s.Type {
	case SettingBool:
		switch strings.ToLower(raw) {
		case "on", "true", "yes":
			return true, nil
		case "off", "false", "no":
			return false, nil
		}
		return nil, fmt.Errorf("%s is a bool: use on or off", s.Key)
	case SettingInt:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%s is a whole number, got %q", s.Key, raw)
		}
		if n < 0 {
			return nil, fmt.Errorf("%s cannot be negative", s.Key)
		}
		return n, nil
	case SettingFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%s is a number, got %q", s.Key, raw)
		}
		if f < 0 {
			return nil, fmt.Errorf("%s cannot be negative", s.Key)
		}
		if r, ok := settingRanges[s.Key]; ok && (f < r.lo || f > r.hi) {
			return nil, fmt.Errorf("%s must be above 0 and at most %g", s.Key, r.hi)
		}
		return f, nil
	default:
		if s.strict && !containsString(s.Values, raw) {
			return nil, fmt.Errorf("%s must be one of %s", s.Key, strings.Join(s.Values, ", "))
		}
		return raw, nil
	}
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// field returns the addressable leaf of cfg this setting names.
func (s Setting) field(v reflect.Value) reflect.Value {
	return v.FieldByIndex(s.index)
}

// Value returns the key's value in cfg, typed as Parse returns it.
func (s Setting) Value(cfg types.Config) any {
	f := s.field(reflect.ValueOf(cfg))
	switch s.Type {
	case SettingBool:
		return f.Bool()
	case SettingInt:
		return int(f.Int())
	case SettingFloat:
		return f.Float()
	default:
		return f.String()
	}
}

// Apply sets the key in cfg to v, a value as Parse returns it.
func (s Setting) Apply(cfg *types.Config, v any) {
	f := s.field(reflect.ValueOf(cfg).Elem())
	switch x := v.(type) {
	case bool:
		f.SetBool(x)
	case int:
		f.SetInt(int64(x))
	case float64:
		f.SetFloat(x)
	case string:
		f.SetString(x)
	}
}

// Format renders the key's value in cfg for display: on/off for bools, and
// quotes around an empty string so "" reads as a value rather than a gap.
// Multi-line values (a custom prompt) are cut to their first line.
func (s Setting) Format(cfg types.Config) string {
	return FormatSettingValue(s.Value(cfg))
}

// FormatSettingValue renders a typed setting value the way Format does.
func FormatSettingValue(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "on"
		}
		return "off"
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		if x == "" {
			return `""`
		}
		if line, _, cut := strings.Cut(strings.TrimSpace(x), "\n"); cut {
			return line + " …"
		}
		return x
	default:
		return fmt.Sprint(v)
	}
}

// FileSettings reads the config file at path and returns what it resolves to
// once nib's defaults fill its gaps, plus the settable keys it sets
// explicitly. It is how /settings tells "from the file" apart from "default".
// A missing file resolves to pure defaults with nothing present.
func FileSettings(path string) (types.Config, map[string]bool, error) {
	present := map[string]bool{}
	var cfg types.Config
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return types.Config{}, nil, err
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return types.Config{}, nil, fmt.Errorf("%s: %w", path, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return types.Config{}, nil, fmt.Errorf("%s: %w", path, err)
		}
		if root := docMapping(&doc); root != nil {
			for _, s := range Settings() {
				if _, v := lookupPath(root, strings.Split(s.Key, ".")); v != nil {
					present[s.Key] = true
				}
			}
		}
	}
	return withDefaults(cfg), present, nil
}

// WriteSetting sets key to v (a value as Setting.Parse returns it) in the
// config file at path, creating the file and any missing parent mappings.
//
// It edits the yaml node tree rather than round-tripping through a struct or
// a map, so comments, key order and keys nib does not know about survive. A
// file that does not parse is refused rather than replaced: it is the user's,
// and whatever is wrong with it is theirs to see.
func WriteSetting(path, key string, v any) error {
	doc, err := readDoc(path)
	if err != nil {
		return err
	}
	root := docMapping(doc)
	segs := strings.Split(key, ".")

	for _, block := range blockDefaulted {
		if segs[0] == block && len(segs) > 1 {
			if _, existing := lookupPath(root, segs[:1]); existing == nil {
				seedBlockDefaults(root, block)
			}
		}
	}

	parent := root
	for _, seg := range segs[:len(segs)-1] {
		parent = childMapping(parent, seg)
	}
	setScalar(parent, segs[len(segs)-1], scalarNode(v))
	return writeDoc(path, doc)
}

// UnsetSetting removes key from the config file at path, so its default
// applies again, and removes any parent mapping the removal left empty.
// It reports whether the key was there. A missing file has nothing to unset.
func UnsetSetting(path, key string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	doc, err := readDoc(path)
	if err != nil {
		return false, err
	}
	root := docMapping(doc)
	if !removePath(root, strings.Split(key, ".")) {
		return false, nil
	}
	return true, writeDoc(path, doc)
}

// seedBlockDefaults writes the defaults withDefaults would have filled for an
// absent block, so the block keeps meaning what it meant once it exists.
func seedBlockDefaults(root *yaml.Node, block string) {
	defaults := withDefaults(types.Config{})
	m := childMapping(root, block)
	for _, s := range Settings() {
		leaf, ok := strings.CutPrefix(s.Key, block+".")
		if !ok {
			continue
		}
		v := s.Value(defaults)
		if reflect.ValueOf(v).IsZero() {
			continue
		}
		setScalar(m, leaf, scalarNode(v))
	}
}

// readDoc parses path into a document whose root is a mapping, starting a
// fresh one for a missing or empty file.
func readDoc(path string) (*yaml.Node, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, doc); err != nil {
			return nil, fmt.Errorf("%s does not parse, not rewriting it: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		// A file of only comments or whitespace decodes to a zero node.
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := doc.Content[0]
	switch {
	case root.Kind == yaml.MappingNode:
	case root.Kind == yaml.ScalarNode && root.Tag == "!!null":
		doc.Content[0] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: root.HeadComment}
	default:
		return nil, fmt.Errorf("%s is not a yaml mapping, not rewriting it", path)
	}
	return doc, nil
}

func docMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return doc.Content[0]
}

// lookupPath walks mappings along segs, returning the key and value nodes of
// the leaf, or nils when any step is missing.
func lookupPath(m *yaml.Node, segs []string) (*yaml.Node, *yaml.Node) {
	for i, seg := range segs {
		if m == nil || m.Kind != yaml.MappingNode {
			return nil, nil
		}
		var next *yaml.Node
		var key *yaml.Node
		for j := 0; j+1 < len(m.Content); j += 2 {
			if m.Content[j].Value == seg {
				key, next = m.Content[j], m.Content[j+1]
				break
			}
		}
		if next == nil {
			return nil, nil
		}
		if i == len(segs)-1 {
			return key, next
		}
		m = next
	}
	return nil, nil
}

// childMapping returns the mapping under key in m, creating it, or replacing
// an empty value (`compaction:` with nothing under it decodes as null).
func childMapping(m *yaml.Node, key string) *yaml.Node {
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value != key {
			continue
		}
		v := m.Content[j+1]
		if v.Kind != yaml.MappingNode {
			nm := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", LineComment: v.LineComment}
			m.Content[j+1] = nm
			return nm
		}
		return v
	}
	nm := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, nm)
	return nm
}

// setScalar sets key in m to v, keeping an existing value's comments.
func setScalar(m *yaml.Node, key string, v *yaml.Node) {
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == key {
			old := m.Content[j+1]
			v.HeadComment, v.LineComment, v.FootComment = old.HeadComment, old.LineComment, old.FootComment
			m.Content[j+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}

// removePath deletes the leaf at segs, then any mapping emptied by it. It
// reports whether the leaf existed.
func removePath(m *yaml.Node, segs []string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value != segs[0] {
			continue
		}
		if len(segs) == 1 {
			m.Content = append(m.Content[:j], m.Content[j+2:]...)
			return true
		}
		child := m.Content[j+1]
		if !removePath(child, segs[1:]) {
			return false
		}
		if child.Kind == yaml.MappingNode && len(child.Content) == 0 {
			m.Content = append(m.Content[:j], m.Content[j+2:]...)
		}
		return true
	}
	return false
}

// scalarNode builds a typed scalar. The tag is explicit so a string that looks
// like another type ("on", "8000") is quoted on the way out and reads back as
// the string it is, and so a bool is written as true/false, the spelling every
// yaml reader agrees on.
func scalarNode(v any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode}
	switch x := v.(type) {
	case bool:
		n.Tag, n.Value = "!!bool", strconv.FormatBool(x)
	case int:
		n.Tag, n.Value = "!!int", strconv.Itoa(x)
	case float64:
		n.Tag, n.Value = "!!float", strconv.FormatFloat(x, 'g', -1, 64)
	default:
		n.Tag, n.Value = "!!str", fmt.Sprint(v)
	}
	return n
}

// writeDoc encodes doc to path atomically (temp file + rename in the same
// directory), creating the directory if needed. It writes through a symlink to
// its target, so a config kept in a dotfiles repo stays linked, and it keeps an
// existing file's permissions. A new file is 0600, since the file this creates
// is the same one api_key lives in.
func writeDoc(path string, doc *yaml.Node) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
