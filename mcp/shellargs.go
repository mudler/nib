package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// scriptArgs is the script argument bash and bash_background share. Script is
// the documented name. Command is an alias: most models are trained on shell
// tools that call it "command", and a schema that rejected it made them decide
// they had no shell at all.
//
// Script carries omitempty so the schema no longer requires it, which is what
// lets a call name only command through validation. resolve then enforces
// that exactly one script is given.
type scriptArgs struct {
	Script  string `json:"script,omitempty" jsonschema:"the shell script to run (required)"`
	Command string `json:"command,omitempty" jsonschema:"alias for script, accepted for compatibility; use script"`
}

var (
	errMissingScript     = errors.New(`missing required argument "script" (the shell script to run)`)
	errConflictingScript = errors.New(`"script" and "command" name different scripts; pass the script in "script" only`)
)

// resolve returns the script a call runs: script, or command when script is
// empty. Both set to different values is an error rather than a guess, because
// approval reads the same resolution (BashScript) and must see exactly the
// script that runs.
func (a scriptArgs) resolve() (string, error) {
	script, command := strings.TrimSpace(a.Script), strings.TrimSpace(a.Command)
	switch {
	case script != "" && command != "" && script != command:
		return "", errConflictingScript
	case script != "":
		return a.Script, nil
	case command != "":
		return a.Command, nil
	}
	return "", errMissingScript
}

// CanonicalBashArgs rewrites the arguments of a bash or bash_background call
// that names its script with the command alias into the documented form:
// "command" becomes "script", and the other arguments are kept. The tool runs
// the same script either way (see resolve).
//
// The approval path applies it before anything reads the arguments, so a
// PreToolUse hook, the approval prompt and the classifier all see "script".
// A user's hook that checks only "script" is then not bypassed by a call that
// uses the alias.
//
// Arguments of other tools, arguments that do not parse, and calls that name
// no script or two different scripts come back unchanged. The handler refuses
// the last two, and every check already treats them as unsafe.
func CanonicalBashArgs(tool, argsJSON string) string {
	if tool != "bash" && tool != "bash_background" {
		return argsJSON
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return argsJSON
	}
	if _, ok := m["command"]; !ok {
		return argsJSON
	}
	script, ok := BashScript(argsJSON)
	if !ok {
		return argsJSON
	}
	enc, err := marshalNoEscape(script)
	if err != nil {
		return argsJSON
	}
	delete(m, "command")
	m["script"] = enc
	out, err := marshalNoEscape(m)
	if err != nil {
		return argsJSON
	}
	return string(out)
}

// BashScript returns the script a bash or bash_background call with these
// JSON arguments runs, resolved exactly as the tool handlers resolve it. ok
// is false when the arguments do not parse or name no single script. Every
// reader of bash arguments outside the handlers (approval, prefix grants,
// read-only checks, display) goes through this, so a call that uses the
// command alias is judged by the script that actually runs.
func BashScript(argsJSON string) (script string, ok bool) {
	var a scriptArgs
	if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
		return "", false
	}
	s, err := a.resolve()
	if err != nil {
		return "", false
	}
	return s, true
}

// marshalNoEscape is json.Marshal without HTML escaping. json.Marshal writes
// &, < and > as \u0026, \u003c and \u003e, which is valid JSON but no longer
// the text the model sent: a hook that matches "&&" or ">" in the raw
// arguments would miss them, and only on calls that used the alias.
func marshalNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
