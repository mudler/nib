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
)

// Action is the resolved result of a submitted input line.
type Action struct {
	Kind  Kind
	Text  string // for KindSend: the message to send
	Skill string // for KindLoadSkill: the skill name
	Err   string // for KindError

	// Loop actions:
	Interval time.Duration // KindLoopStart: 0 = self-paced
	Payload  string        // KindLoopStart: the prompt/slash-command to repeat
	LoopID   string        // KindLoopStop: empty = stop all
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
		return Action{Kind: KindSend, Text: input}
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
	case "loop":
		return resolveLoop(rest)
	case "goal":
		return resolveGoal(rest)
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
