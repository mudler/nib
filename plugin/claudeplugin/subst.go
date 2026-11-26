package claudeplugin

import (
	"regexp"
	"strconv"
	"strings"
)

// Vars holds the values substituted into plugin content and configuration.
// An empty field leaves its ${...} token as written.
type Vars struct {
	PluginRoot string // ${CLAUDE_PLUGIN_ROOT}
	PluginData string // ${CLAUDE_PLUGIN_DATA}
	ProjectDir string // ${CLAUDE_PROJECT_DIR}
	SkillDir   string // ${CLAUDE_SKILL_DIR}, for a skill body
	SessionID  string // ${CLAUDE_SESSION_ID}

	// UserConfig holds the saved userConfig values, as strings, for
	// ${user_config.KEY}. Sensitive marks the keys whose value must not
	// appear in model-visible content.
	UserConfig map[string]string
	Sensitive  map[string]bool

	// Env resolves ${VAR} and ${VAR:-default} in configuration (MCP, LSP,
	// hook commands). Nil leaves those tokens as written.
	Env func(string) (string, bool)
}

var varToken = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_.]*)(:-[^}]*)?\}`)

// ExpandContent substitutes the plugin variables in a skill, command or agent
// body: ${CLAUDE_PLUGIN_ROOT}, ${CLAUDE_PLUGIN_DATA}, ${CLAUDE_PROJECT_DIR},
// ${CLAUDE_SKILL_DIR}, ${CLAUDE_SESSION_ID} and ${user_config.KEY}. A
// sensitive user_config value becomes a placeholder. Other ${...} tokens,
// such as shell variables in a code sample, stay as written.
func ExpandContent(s string, v Vars) string {
	return expand(s, v, false, nil)
}

// ExpandConfig substitutes the same variables as [ExpandContent] in a
// configuration value (MCP url, header, command, args, env; LSP command),
// with sensitive user_config values included, and resolves ${VAR} and
// ${VAR:-default} through v.Env when it is set. Variables that Env does not
// know and that have no default become empty and are appended to missing.
func ExpandConfig(s string, v Vars, missing *[]string) string {
	return expand(s, v, true, missing)
}

func expand(s string, v Vars, config bool, missing *[]string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return varToken.ReplaceAllStringFunc(s, func(tok string) string {
		m := varToken.FindStringSubmatch(tok)
		name, def := m[1], m[2]
		if val, ok := pluginVar(name, v); ok {
			return val
		}
		if key, ok := strings.CutPrefix(name, "user_config."); ok {
			val, ok := v.UserConfig[key]
			if !ok {
				return tok
			}
			if !config && v.Sensitive[key] {
				return "[sensitive option " + key + "]"
			}
			return val
		}
		if !config || v.Env == nil || strings.Contains(name, ".") {
			return tok
		}
		if val, ok := v.Env(name); ok && (val != "" || def == "") {
			return val
		}
		if def != "" {
			return strings.TrimPrefix(def, ":-")
		}
		if missing != nil {
			*missing = append(*missing, name)
		}
		return ""
	})
}

func pluginVar(name string, v Vars) (string, bool) {
	var val string
	switch name {
	case "CLAUDE_PLUGIN_ROOT":
		val = v.PluginRoot
	case "CLAUDE_PLUGIN_DATA":
		val = v.PluginData
	case "CLAUDE_PROJECT_DIR":
		val = v.ProjectDir
	case "CLAUDE_SKILL_DIR":
		val = v.SkillDir
	case "CLAUDE_SESSION_ID":
		val = v.SessionID
	default:
		return "", false
	}
	return val, val != ""
}

// RenderArguments substitutes invocation arguments into a command or skill
// body:
//
//   - $ARGUMENTS is the whole argument string as typed;
//   - $ARGUMENTS[N] and $N are the Nth argument, 0-based, split with shell
//     quoting rules; a missing one stays as written;
//   - $name is the argument at the position of name in names (the
//     "arguments" frontmatter); a missing one becomes empty;
//   - a backslash directly before a token keeps it literal ("\$1" gives "$1").
//
// When the body has none of these placeholders and args is not empty, the
// arguments are appended as "ARGUMENTS: <args>". ${...} tokens are left for
// [ExpandContent].
func RenderArguments(body, args string, names []string) string {
	argv := SplitArgs(args)
	named := make(map[string]int, len(names))
	for i, n := range names {
		named[n] = i
	}
	var b strings.Builder
	used := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) && body[i+1] == '$' {
			if _, n := argToken(body[i+1:], named); n > 0 {
				b.WriteString(body[i+1 : i+1+n])
				i += n
				continue
			}
		}
		if c != '$' {
			b.WriteByte(c)
			continue
		}
		tok, n := argToken(body[i:], named)
		if n == 0 {
			b.WriteByte(c)
			continue
		}
		used = true
		switch {
		case tok.all:
			b.WriteString(args)
		case tok.index >= 0:
			if tok.index < len(argv) {
				b.WriteString(argv[tok.index])
			} else {
				b.WriteString(body[i : i+n])
			}
		default:
			if tok.named < len(argv) {
				b.WriteString(argv[tok.named])
			}
		}
		i += n - 1
	}
	out := b.String()
	if !used && strings.TrimSpace(args) != "" {
		out = strings.TrimRight(out, "\n") + "\n\nARGUMENTS: " + args + "\n"
	}
	return out
}

type argTok struct {
	all   bool
	index int
	named int
}

// argToken reads the argument token at the start of s (which starts with
// '$'). It returns its length, or 0 when s does not start with one.
func argToken(s string, named map[string]int) (argTok, int) {
	if len(s) < 2 || s[0] != '$' {
		return argTok{}, 0
	}
	rest := s[1:]
	if d := leadingDigits(rest); d > 0 {
		n, _ := strconv.Atoi(rest[:d])
		return argTok{index: n}, 1 + d
	}
	id := leadingIdent(rest)
	if id == "" {
		return argTok{}, 0
	}
	if strings.HasPrefix(id, "ARGUMENTS") {
		id = "ARGUMENTS"
		after := rest[len(id):]
		if strings.HasPrefix(after, "[") {
			if d := leadingDigits(after[1:]); d > 0 && len(after) > 1+d && after[1+d] == ']' {
				n, _ := strconv.Atoi(after[1 : 1+d])
				return argTok{index: n}, 1 + len(id) + d + 2
			}
		}
		return argTok{all: true, index: -1}, 1 + len(id)
	}
	if pos, ok := named[id]; ok {
		return argTok{index: -1, named: pos}, 1 + len(id)
	}
	return argTok{}, 0
}

func leadingDigits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

func leadingIdent(s string) string {
	n := 0
	for n < len(s) {
		c := s[n]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || n > 0 && c >= '0' && c <= '9' {
			n++
			continue
		}
		break
	}
	return s[:n]
}

// SplitArgs splits an argument string with shell quoting rules: whitespace
// separates words, single quotes keep text literal, double quotes keep spaces
// and honour backslash escapes, and a backslash outside quotes escapes the
// next character. An unclosed quote runs to the end of the string.
func SplitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			inWord = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				cur.WriteString(s[i+1:])
				i = len(s)
				continue
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			inWord = true
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(`"\$`+"`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
		case c == '\\' && i+1 < len(s):
			inWord = true
			i++
			cur.WriteByte(s[i])
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}
