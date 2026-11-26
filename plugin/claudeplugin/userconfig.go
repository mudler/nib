package claudeplugin

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// User configuration option types.
const (
	OptionString    = "string"
	OptionNumber    = "number"
	OptionBoolean   = "boolean"
	OptionDirectory = "directory"
	OptionFile      = "file"
)

// UserConfigOption is one typed userConfig option that a host asks the user
// for when the plugin is enabled.
type UserConfigOption struct {
	Key         string   `json:"-"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Multiple    bool     `json:"multiple,omitempty"`
	// Sensitive values must be stored as secrets and never placed in
	// model-visible content.
	Sensitive bool     `json:"sensitive,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
}

var optionKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (l *loader) loadUserConfig(raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var opts map[string]UserConfigOption
	if err := json.Unmarshal(raw, &opts); err != nil {
		l.p.skip("manifest", "userConfig", err.Error())
		return
	}
	for _, k := range slices.Sorted(maps.Keys(opts)) {
		o := opts[k]
		o.Key = k
		if !optionKey.MatchString(k) {
			l.p.skip("manifest", "userConfig."+k, "option keys use letters, digits and '_'")
			continue
		}
		switch o.Type {
		case OptionString, OptionNumber, OptionBoolean, OptionDirectory, OptionFile:
		default:
			l.p.skip("manifest", "userConfig."+k, fmt.Sprintf("unknown option type %q", o.Type))
			continue
		}
		if l.p.UserConfig == nil {
			l.p.UserConfig = map[string]UserConfigOption{}
		}
		l.p.UserConfig[k] = o
	}
}

// Validate checks a value against the option. A nil value is valid unless
// the option is required.
func (o UserConfigOption) Validate(v any) error {
	if v == nil || v == "" {
		if o.Required {
			return fmt.Errorf("option %s is required", o.Key)
		}
		return nil
	}
	switch o.Type {
	case OptionBoolean:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("option %s must be a boolean", o.Key)
		}
	case OptionNumber:
		f, ok := toFloat(v)
		if !ok {
			return fmt.Errorf("option %s must be a number", o.Key)
		}
		if o.Min != nil && f < *o.Min || o.Max != nil && f > *o.Max {
			return fmt.Errorf("option %s is out of range", o.Key)
		}
	default:
		vals, err := o.strings(v)
		if err != nil {
			return err
		}
		if len(o.Options) > 0 {
			for _, s := range vals {
				if !slices.Contains(o.Options, s) {
					return fmt.Errorf("option %s: %q is not one of %v", o.Key, s, o.Options)
				}
			}
		}
	}
	return nil
}

func (o UserConfigOption) strings(v any) ([]string, error) {
	switch x := v.(type) {
	case string:
		return []string{x}, nil
	case []string:
		if !o.Multiple {
			return nil, fmt.Errorf("option %s takes one value", o.Key)
		}
		return x, nil
	case []any:
		if !o.Multiple {
			return nil, fmt.Errorf("option %s takes one value", o.Key)
		}
		out := make([]string, 0, len(x))
		for _, it := range x {
			s, ok := it.(string)
			if !ok {
				return nil, fmt.Errorf("option %s takes strings", o.Key)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("option %s must be a string", o.Key)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

// ResolveUserConfig merges saved values over the option defaults, validates
// each one and returns them as strings for [Vars].UserConfig and
// [OptionEnv]. A list is joined with ','. Keys the plugin does not declare
// are ignored.
func (p *Plugin) ResolveUserConfig(values map[string]any) (map[string]string, error) {
	out := map[string]string{}
	for k, o := range p.UserConfig {
		v, ok := values[k]
		if !ok || v == nil {
			v = o.Default
		}
		if err := o.Validate(v); err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		out[k] = optionString(v)
	}
	return out, nil
}

func optionString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case []string:
		return strings.Join(x, ",")
	case []any:
		parts := make([]string, len(x))
		for i, it := range x {
			parts[i] = fmt.Sprint(it)
		}
		return strings.Join(parts, ",")
	}
	if f, ok := toFloat(v); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// SensitiveKeys returns the keys of the sensitive options, for
// [Vars].Sensitive.
func (p *Plugin) SensitiveKeys() map[string]bool {
	out := map[string]bool{}
	for k, o := range p.UserConfig {
		if o.Sensitive {
			out[k] = true
		}
	}
	return out
}

// OptionEnv returns the CLAUDE_PLUGIN_OPTION_<KEY>=value entries that a hook
// process receives, sorted by key.
func OptionEnv(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, k := range slices.Sorted(maps.Keys(values)) {
		out = append(out, "CLAUDE_PLUGIN_OPTION_"+strings.ToUpper(k)+"="+values[k])
	}
	return out
}
