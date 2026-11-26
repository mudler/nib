package claudeplugin

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SplitFrontmatter separates a markdown file's leading "---" YAML block from
// its body. Without a closed block, fm is nil and body is the whole input.
func SplitFrontmatter(data []byte) (fm []byte, body string) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return nil, string(data)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "---" {
			return []byte(strings.Join(lines[1:i], "")), strings.Join(lines[i+1:], "")
		}
	}
	return nil, string(data)
}

// frontmatter is a parsed YAML header with typed accessors. Keys are kept as
// written.
type frontmatter map[string]any

// parseFrontmatter decodes fm. When strict YAML fails (an unquoted colon in a
// description is common), it falls back to reading top-level "key: value"
// lines and reports the YAML error as a warning.
func parseFrontmatter(fm []byte) (frontmatter, error) {
	if len(bytes.TrimSpace(fm)) == 0 {
		return frontmatter{}, nil
	}
	var m map[string]any
	err := yaml.Unmarshal(fm, &m)
	if err == nil {
		if m == nil {
			m = map[string]any{}
		}
		return frontmatter(m), nil
	}
	return lenientFrontmatter(fm), fmt.Errorf("frontmatter is not valid YAML, read line by line: %w", err)
}

func lenientFrontmatter(fm []byte) frontmatter {
	out := frontmatter{}
	var lastKey string
	for _, line := range strings.Split(string(fm), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			// continuation of a folded value
			if lastKey != "" {
				if s, ok := out[lastKey].(string); ok {
					out[lastKey] = strings.TrimSpace(s + " " + strings.TrimSpace(line))
				}
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if v == ">" || v == "|" || v == ">-" || v == "|-" {
			v = ""
		}
		out[k] = v
		lastKey = k
	}
	return out
}

func (f frontmatter) has(key string) bool {
	_, ok := f[key]
	return ok
}

func (f frontmatter) str(key string) string {
	switch v := f[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func (f frontmatter) boolean(key string) (bool, bool) {
	switch v := f[key].(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return b, err == nil
	}
	return false, false
}

func (f frontmatter) integer(key string) int {
	switch v := f[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

// list reads a YAML list or a string. A string is split on commas outside
// parentheses when it has any, otherwise on whitespace outside parentheses, so
// "Bash(git add:*), Read" and "Read Grep" both work.
func (f frontmatter) list(key string) []string {
	switch v := f[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, it := range v {
			if s := strings.TrimSpace(fmt.Sprint(it)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return SplitToolList(v)
	}
	return nil
}

// SplitToolList splits a tools string from frontmatter. Commas outside
// parentheses separate entries when present; otherwise whitespace outside
// parentheses does.
func SplitToolList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	sep := func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }
	if hasTopLevel(s, ',') {
		sep = func(r rune) bool { return r == ',' }
	}
	var out []string
	var cur strings.Builder
	depth := 0
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, t)
		}
		cur.Reset()
	}
	for _, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth == 0 && sep(r):
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

func hasTopLevel(s string, c rune) bool {
	depth := 0
	for _, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case r == c && depth == 0:
			return true
		}
	}
	return false
}
