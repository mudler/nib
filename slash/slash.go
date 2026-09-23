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

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

// Kind enumerates the resolved action types.
type Kind int

// Command verbs — typed constants to avoid string literals scattered
// through the switch in Resolve and its helpers.
const (
	cmdSkill      = "skill"
	cmdAgent      = "agent"
	cmdCompact    = "compact"
	cmdModels     = "models"
	cmdModel      = "model"
	cmdLoop       = "loop"
	cmdYolo       = "yolo"
	cmdApprove    = "approve"
	cmdClassifier = "classifier"
	cmdGoal       = "goal"
	cmdResume     = "resume"
	cmdLogin      = "login"
	cmdLogout     = "logout"
	cmdAttach     = "attach"
	cmdSettings   = "settings"
	cmdEndpoint   = "endpoint"
	cmdAbout      = "about"

	// /attach sub-verbs
	cmdAttachClear = "clear"

	// /loop sub-verbs
	cmdLoopStop = "stop"
	cmdLoopList = "list"

	// /goal sub-verbs
	cmdGoalClear  = "clear"
	cmdGoalResume = "resume"

	// /model sub-verbs
	cmdModelReset = "reset"

	// /yolo sub-verbs
	cmdYoloOn  = "on"
	cmdYoloOff = "off"

	// /resume flags
	flagResumeAll = "--all"

	// /settings value words that remove the key from the config file, so
	// its default applies again.
	settingDefault = "default"
	settingUnset   = "unset"
)

const (
	KindSend       Kind = iota // send Text to the agent
	KindLoadSkill              // eagerly load Skill into the session prompt
	KindError                  // report Err to the user, send nothing
	KindCompact                // compact the current conversation
	KindLoopStart              // start a recurring/self-paced loop
	KindLoopStop               // stop one loop (LoopID) or all (empty)
	KindLoopList               // list active loops
	KindGoalSet                // set/replace the session goal (Text)
	KindGoalShow               // show the current goal
	KindGoalClear              // clear the current goal
	KindGoalResume             // resume a goal an interrupt paused
	KindAttach                 // stage/list/clear file attachments
	KindModelList              // list available models
	KindModelPick              // pick a model from the available models
	KindModelSet               // switch the session model to Model
	KindYolo                   // toggle (or explicitly set) session-wide auto-approval
	KindResume                 // resume a recorded session (ResumeID) or open the picker
	KindLogin                  // log in to a provider (Provider empty = list)
	KindLogout                 // log out of a provider (Provider empty = list)
	KindSettings               // list, show, set or unset a config key (SettingKey etc.)
	KindEndpoint               // switch endpoint (Endpoint empty = open the picker)
	KindModelReset             // drop the saved model override for the current endpoint
	KindAbout                  // print version, config paths, and tool inventory
	KindApprove                // set the approval mode (Mode), or show it when Mode is empty
	KindClassifier             // set the classifier (Endpoint, Model), turn it off, or pick one
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
	Kind   Kind
	Text   string             // for KindSend: the message to send
	Skill  string             // for KindLoadSkill: the skill name
	Err    string             // for KindError
	Model  string             // for KindModelSet: the model to switch to
	YoloOn *bool              // for KindYolo: nil = toggle, non-nil = set explicitly (on/off)
	Mode   types.ApprovalMode // for KindApprove: the approval mode, empty = show the current one
	// ClassifierOff is /classifier off. KindClassifier also uses Endpoint
	// and Model; both empty opens the picker.
	ClassifierOff bool

	// Resume actions:
	ResumeAll bool   // KindResume: widen the picker to sessions from any cwd
	ResumeID  string // KindResume: non-empty loads this session directly, skipping the picker

	// Loop actions:
	Interval time.Duration // KindLoopStart: 0 = self-paced
	Payload  string        // KindLoopStart: the prompt/slash-command to repeat
	LoopID   string        // KindLoopStop: empty = stop all

	// Attachment actions:
	Files      []string // KindSend: resolved @path attachments
	AttachOp   AttachOp // KindAttach: which op
	AttachPath string   // KindAttach+AttachStage: file to stage
	Transcribe bool     // KindAttach+AttachStage: --transcribe/-t override

	// Login/logout actions:
	Provider string // KindLogin/KindLogout: provider ID; empty = list

	// Settings actions (KindSettings). An empty SettingKey lists every key; a
	// key alone shows it; a key with a value sets it. The value stays raw text
	// here: typing and validation need the key's type, which lives in config,
	// and slash has no business knowing the config schema.
	SettingKey      string
	SettingValue    string // raw, possibly containing spaces
	SettingHasValue bool   // a value was given (distinguishes an empty string)
	SettingUnset    bool   // the value was "default" or "unset": remove the key

	// Endpoint actions:
	Endpoint string // KindEndpoint: endpoint ID; empty = picker.
	//                KindModelList: which endpoint to list; empty = current.
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
	case cmdSkill:
		name, _ := splitVerb(rest)
		if name == "" {
			return Action{Kind: KindError, Err: "usage: /skill <name>"}
		}
		if !hasSkill(skills, name) {
			return Action{Kind: KindError, Err: fmt.Sprintf("unknown skill %q", name)}
		}
		return Action{Kind: KindLoadSkill, Skill: name}
	case cmdAgent:
		name, task := splitVerb(rest)
		if name == "" {
			return Action{Kind: KindError, Err: "usage: /agent <name> <task>"}
		}
		if !hasAgent(agents, name) {
			return Action{Kind: KindError, Err: fmt.Sprintf("unknown agent %q", name)}
		}
		return Action{Kind: KindSend, Text: delegation(name, task)}
	case cmdCompact:
		return Action{Kind: KindCompact}
	case cmdModels:
		return Action{Kind: KindModelList, Endpoint: strings.TrimSpace(rest)}
	case cmdModel:
		name := strings.TrimSpace(rest)
		switch name {
		case "":
			return Action{Kind: KindModelPick}
		case cmdModelReset:
			return Action{Kind: KindModelReset}
		}
		return Action{Kind: KindModelSet, Model: name}
	case cmdLoop:
		return resolveLoop(rest)
	case cmdYolo:
		switch strings.ToLower(strings.TrimSpace(rest)) {
		case "":
			return Action{Kind: KindYolo} // nil YoloOn = toggle
		case cmdYoloOn:
			on := true
			return Action{Kind: KindYolo, YoloOn: &on}
		case cmdYoloOff:
			off := false
			return Action{Kind: KindYolo, YoloOn: &off}
		default:
			return Action{Kind: KindError, Err: theme.YoloUsage}
		}
	case cmdApprove:
		if strings.TrimSpace(rest) == "" {
			return Action{Kind: KindApprove}
		}
		mode, ok := types.ParseApprovalMode(rest)
		if !ok {
			return Action{Kind: KindError, Err: theme.ApproveUsage}
		}
		return Action{Kind: KindApprove, Mode: mode}
	case cmdClassifier:
		args := strings.Fields(rest)
		switch {
		case len(args) == 0:
			return Action{Kind: KindClassifier}
		case len(args) == 1 && strings.EqualFold(args[0], "off"):
			return Action{Kind: KindClassifier, ClassifierOff: true}
		case len(args) == 1:
			return Action{Kind: KindClassifier, Endpoint: args[0]}
		case len(args) == 2:
			return Action{Kind: KindClassifier, Endpoint: args[0], Model: args[1]}
		default:
			return Action{Kind: KindError, Err: theme.ClassifierUsage}
		}
	case cmdGoal:
		return resolveGoal(rest)
	case cmdResume:
		return resolveResume(rest)
	case cmdLogin:
		return Action{Kind: KindLogin, Provider: strings.TrimSpace(rest)}
	case cmdLogout:
		return Action{Kind: KindLogout, Provider: strings.TrimSpace(rest)}
	case cmdSettings:
		return resolveSettings(rest)
	case cmdAttach:
		rest = strings.TrimSpace(rest)
		switch {
		case rest == "":
			return Action{Kind: KindAttach, AttachOp: AttachList}
		case rest == cmdAttachClear:
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
	case cmdEndpoint:
		return Action{Kind: KindEndpoint, Endpoint: strings.TrimSpace(rest)}
	case cmdAbout:
		return Action{Kind: KindAbout}
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
	case cmdLoopStop:
		return Action{Kind: KindLoopStop, LoopID: strings.TrimSpace(after)}
	case cmdLoopList:
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
// "/goal clear" clears, "/goal resume" resumes a goal an interrupt paused.
func resolveGoal(rest string) Action {
	rest = strings.TrimSpace(rest)
	switch rest {
	case "":
		return Action{Kind: KindGoalShow}
	case cmdGoalClear:
		return Action{Kind: KindGoalClear}
	case cmdGoalResume:
		return Action{Kind: KindGoalResume}
	}
	return Action{Kind: KindGoalSet, Text: rest}
}

// resolveResume maps the /resume subcommands: "/resume" opens the picker
// (cwd-scoped), "/resume --all" opens it widened to every recorded session,
// and "/resume <id>" (optionally combined with --all, though an explicit id
// never needs the widened list to find it) loads that session directly. The
// two tokens are independent flags rather than positional args, so either
// order ("/resume --all abc123" or "/resume abc123 --all") resolves the
// same way.
func resolveResume(rest string) Action {
	all := false
	id := ""
	for _, tok := range strings.Fields(rest) {
		if tok == flagResumeAll {
			all = true
			continue
		}
		if id == "" {
			id = tok
		}
	}
	return Action{Kind: KindResume, ResumeAll: all, ResumeID: id}
}

// resolveSettings maps the /settings forms: "/settings" lists, "/settings
// <key>" shows one key, "/settings <key> <value>" sets it, and a value of
// "default" or "unset" removes the key from the file. Everything after the key
// is the value, inner spaces kept, so a string setting can hold a phrase.
//
// The key is not checked here. Whether it exists, and whether the value fits
// its type, is config's knowledge; the TUI asks it and reports the answer.
func resolveSettings(rest string) Action {
	key, value := splitVerb(rest)
	a := Action{Kind: KindSettings, SettingKey: key}
	if value == "" {
		return a
	}
	switch strings.ToLower(value) {
	case settingDefault, settingUnset:
		a.SettingUnset = true
	default:
		a.SettingValue, a.SettingHasValue = value, true
	}
	return a
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
