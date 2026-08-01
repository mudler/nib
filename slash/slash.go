// Package slash resolves a submitted TUI input line into an action: send text
// to the agent, eagerly load a skill, or report an error. It also expands a
// command's prompt template.
package slash

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/mudler/nib/types"
)

// Kind enumerates the resolved action types.
type Kind int

const (
	KindSend      Kind = iota // send Text to the agent
	KindLoadSkill             // eagerly load Skill into the session prompt
	KindError                 // report Err to the user, send nothing
	KindCompact               // compact the current conversation
	KindLoopStart             // start a recurring/self-paced loop
	KindLoopStop              // stop one loop (LoopID) or all (empty)
	KindLoopList              // list active loops
	KindGoalSet               // set/replace the session goal (Text)
	KindGoalShow              // show the current goal
	KindGoalClear             // clear the current goal
	KindAttach                // stage/list/clear file attachments
	KindModelList             // list available models
	KindModelSet              // switch the session model to Model
)

// AttachOp enumerates the /attach sub-operations.
type AttachOp int

const (
	AttachStage AttachOp = iota // stage AttachPath (optionally Transcribe)
	AttachList                  // list staged attachments
	AttachClear                 // clear staged attachments
)

// Action is the resolved result of a submitted input line.
type Action struct {
	Kind  Kind
	Text  string // for KindSend: the message to send
	Skill string // for KindLoadSkill: the skill name
	Err   string // for KindError
	Model string // for KindModelSet: the model to switch to

	// Loop actions:
	Interval time.Duration // KindLoopStart: 0 = self-paced
	Payload  string        // KindLoopStart: the prompt/slash-command to repeat
	LoopID   string        // KindLoopStop: empty = stop all

	// Attachment actions:
	Files      []string // KindSend: resolved @path attachments
	AttachOp   AttachOp // KindAttach: which op
	AttachPath string   // KindAttach+AttachStage: file to stage
	Transcribe bool     // KindAttach+AttachStage: --transcribe/-t override
}

// Expand renders a command's prompt template with the given args.
func Expand(c types.CommandConfig, args string) (string, error) {
	tmpl, err := template.New("cmd").Parse(c.Prompt)
	if err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	var b bytes.Buffer
	if err := tmpl.Execute(&b, struct {
		Args             string
		CurrentDirectory string
	}{Args: args, CurrentDirectory: cwd}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Resolve maps an input line to an Action. Non-slash input is sent verbatim.
func Resolve(input string, cmds []types.CommandConfig, skills []types.Skill, agents []types.AgentTypeConfig) Action {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		text, files := parseAtPaths(input)
		return Action{Kind: KindSend, Text: text, Files: files}
	}

	verb, rest := splitVerb(trimmed[1:])

	switch verb {
	case "skill":
		name, _ := splitVerb(rest)
		if name == "" {
			return Action{Kind: KindError, Err: "usage: /skill <name>"}
		}
		if !hasSkill(skills, name) {
			return Action{Kind: KindError, Err: fmt.Sprintf("unknown skill %q", name)}
		}
		return Action{Kind: KindLoadSkill, Skill: name}
	case "agent":
		name, task := splitVerb(rest)
		if name == "" {
			return Action{Kind: KindError, Err: "usage: /agent <name> <task>"}
		}
		if !hasAgent(agents, name) {
			return Action{Kind: KindError, Err: fmt.Sprintf("unknown agent %q", name)}
		}
		return Action{Kind: KindSend, Text: delegation(name, task)}
	case "compact":
		return Action{Kind: KindCompact}
	case "models":
		return Action{Kind: KindModelList}
	case "model":
		// Bare /model lists too, so a user who forgets the name gets the
		// menu instead of an error.
		name := strings.TrimSpace(rest)
		if name == "" {
			return Action{Kind: KindModelList}
		}
		return Action{Kind: KindModelSet, Model: name}
	case "loop":
		return resolveLoop(rest)
	case "goal":
		return resolveGoal(rest)
	case "attach":
		rest = strings.TrimSpace(rest)
		switch {
		case rest == "":
			return Action{Kind: KindAttach, AttachOp: AttachList}
		case rest == "clear":
			return Action{Kind: KindAttach, AttachOp: AttachClear}
		default:
			transcribe := false
			if f, ok := strings.CutPrefix(rest, "--transcribe "); ok {
				transcribe, rest = true, strings.TrimSpace(f)
			} else if f, ok := strings.CutPrefix(rest, "-t "); ok {
				transcribe, rest = true, strings.TrimSpace(f)
			}
			if _, err := os.Stat(rest); err != nil {
				return Action{Kind: KindError, Err: "no such file: " + rest}
			}
			return Action{Kind: KindAttach, AttachOp: AttachStage, AttachPath: rest, Transcribe: transcribe}
		}
	default:
		c, ok := findCommand(cmds, verb)
		if !ok {
			return Action{Kind: KindError, Err: fmt.Sprintf("unknown command %q", verb)}
		}
		text, err := Expand(c, rest)
		if err != nil {
			return Action{Kind: KindError, Err: fmt.Sprintf("command %q: %v", verb, err)}
		}
		if strings.TrimSpace(c.Agent) != "" {
			text = delegation(c.Agent, text)
		}
		return Action{Kind: KindSend, Text: text}
	}
}

// loopFloor is the minimum fixed interval; shorter requests are clamped up.
// 1s matches the ~1s scheduler poll, which is the real precision floor.
const loopFloor = 1 * time.Second

func resolveLoop(rest string) Action {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return Action{Kind: KindError, Err: "usage: /loop [interval] <prompt|/command> · /loop stop [id] · /loop list"}
	}
	first, after := splitVerb(rest)
	switch first {
	case "stop":
		return Action{Kind: KindLoopStop, LoopID: strings.TrimSpace(after)}
	case "list":
		return Action{Kind: KindLoopList}
	}
	// Fixed interval if the first token parses as a duration.
	if d, err := time.ParseDuration(first); err == nil {
		payload := strings.TrimSpace(after)
		if payload == "" {
			return Action{Kind: KindError, Err: "usage: /loop " + first + " <prompt|/command>"}
		}
		if d < loopFloor {
			d = loopFloor
		}
		return Action{Kind: KindLoopStart, Interval: d, Payload: payload}
	}
	// Otherwise self-paced: the whole remainder is the payload.
	return Action{Kind: KindLoopStart, Interval: 0, Payload: rest}
}

// resolveGoal maps the /goal subcommands: "/goal <text>" sets, "/goal" shows,
// "/goal clear" clears.
func resolveGoal(rest string) Action {
	rest = strings.TrimSpace(rest)
	switch rest {
	case "":
		return Action{Kind: KindGoalShow}
	case "clear":
		return Action{Kind: KindGoalClear}
	}
	return Action{Kind: KindGoalSet, Text: rest}
}

// parseAtPaths splits a send line into literal text and @path attachments. A
// @token is attached only if it resolves to an existing file (cwd-relative or
// absolute); @"quoted paths" are supported; unmatched @tokens stay literal.
func parseAtPaths(input string) (string, []string) {
	var files []string
	var out strings.Builder
	i := 0
	for i < len(input) {
		atBoundary := i == 0 || input[i-1] == ' ' || input[i-1] == '\t' || input[i-1] == '\n'
		if input[i] == '@' && atBoundary && i+1 < len(input) {
			tok, next := readToken(input, i+1)
			if tok != "" {
				if _, err := os.Stat(tok); err == nil {
					files = append(files, tok)
					i = next
					continue // drop the token from the text
				}
			}
		}
		out.WriteByte(input[i])
		i++
	}
	return strings.TrimSpace(out.String()), files
}

// readToken reads a path token starting at j: a "quoted string" (up to the
// closing quote) or an unquoted run up to whitespace. Returns the token and the
// index just past it.
func readToken(s string, j int) (string, int) {
	if j < len(s) && s[j] == '"' {
		k := strings.IndexByte(s[j+1:], '"')
		if k >= 0 {
			return s[j+1 : j+1+k], j + 1 + k + 1
		}
		return "", j
	}
	k := j
	for k < len(s) && s[k] != ' ' && s[k] != '\t' && s[k] != '\n' {
		k++
	}
	return s[j:k], k
}

// delegation builds a directive instructing the agent to delegate to a named
// sub-agent (the runtime already exposes spawn_agent + the agent-type list).
func delegation(agent, task string) string {
	return fmt.Sprintf("Use the %q sub-agent (spawn_agent) to handle the following task, then report its result:\n\n%s", agent, task)
}

// splitVerb splits s into the first whitespace-delimited token and the rest.
func splitVerb(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func hasSkill(skills []types.Skill, name string) bool {
	for _, s := range skills {
		if s.Name == name {
			return true
		}
	}
	return false
}

func hasAgent(agents []types.AgentTypeConfig, name string) bool {
	for _, a := range agents {
		if a.Name == name {
			return true
		}
	}
	return false
}

func findCommand(cmds []types.CommandConfig, name string) (types.CommandConfig, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return types.CommandConfig{}, false
}
