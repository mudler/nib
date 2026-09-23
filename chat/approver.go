package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/nib/classify"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// maxApproverState caps the text sent to the classifier for one call: about
// what a small encoder reads. A call that does not fit is not judged at all,
// since the part the classifier never saw could do anything.
const maxApproverState = 2 << 10

// cannotJudge is why a call was not shown to the classifier.
type cannotJudge string

func (c cannotJudge) Error() string { return string(c) }

const (
	errTooLong  cannotJudge = "too long to judge"
	errCompound cannotJudge = "compound command"
)

// Default auto_approve policy.
var defaultAutoApproveAllow = []string{"inspect", "build_test"}

const defaultAutoApproveThreshold = 0.85

// categoryDescriptions are the choice options the classifier picks from.
// The descriptions are the zero-shot labels, so they are written as the
// thing to find in the call.
var categoryDescriptions = map[string]string{
	"inspect":     "only reads or lists files or state, changes nothing",
	"build_test":  "runs a build, tests, a formatter or a linter",
	"local_edit":  "modifies files in the workspace, reversible with git",
	"destructive": "deletes, overwrites, force-pushes or resets data",
	"network":     "sends data out, installs packages, pushes or publishes",
	"system":      "uses sudo, touches files outside the workspace, services or credentials",
}

// Verdict is the classifier's opinion of one tool call.
type Verdict struct {
	Category   string
	Confidence float64
	Approved   bool
	Err        error
}

// String is the verdict as the approval prompt shows it.
func (v Verdict) String() string {
	var cj cannotJudge
	if errors.As(v.Err, &cj) {
		return string(cj)
	}
	if v.Err != nil || v.Category == "" {
		return "unavailable"
	}
	return fmt.Sprintf("%s (%.2f)", v.Category, v.Confidence)
}

// Approver applies the auto_approve policy to tool calls with a classifier.
type Approver struct {
	c         classify.Classifier
	allow     []string
	threshold float64
	workDir   string
}

// NewApprover returns an approver for c. Zero cfg fields take the defaults.
func NewApprover(c classify.Classifier, cfg types.AutoApproveConfig, workDir string) *Approver {
	a := &Approver{c: c, allow: cfg.Allow, threshold: cfg.Threshold, workDir: workDir}
	if len(a.allow) == 0 {
		a.allow = defaultAutoApproveAllow
	}
	if a.threshold <= 0 {
		a.threshold = defaultAutoApproveThreshold
	}
	return a
}

// Judge classifies req. It approves only when the top category is allowed
// and its confidence reaches the threshold; any error leaves it unapproved.
func (a *Approver) Judge(ctx context.Context, req ToolCallRequest) Verdict {
	state, err := a.state(req)
	if err != nil {
		xlog.Debug("classifier skipped", "tool", req.Name, "reason", err)
		return Verdict{Err: err}
	}
	ans, err := a.c.Classify(ctx, state, map[string]classify.Question{
		"category": {
			Type:         classify.TypeChoice,
			Instructions: "What does this tool call do?",
			Choices:      categoryDescriptions,
		},
	})
	var v Verdict
	if err != nil {
		v.Err = err
	} else {
		cat := ans["category"]
		v.Category, v.Confidence = cat.Choice, cat.Confidence
		v.Approved = slices.Contains(a.allow, v.Category) && v.Confidence >= a.threshold
	}
	xlog.Debug("classifier verdict", "tool", req.Name, "category", v.Category,
		"confidence", v.Confidence, "approved", v.Approved, "error", v.Err)
	return v
}

// state renders the call as the text the classifier reads: the tool, what
// it runs, where, and why the model says it wants it. It refuses a call the
// classifier could not see whole: one longer than maxApproverState, or a bash
// script that is not one simple command, which would get one category for
// all of its parts. The reason alone is shortened to fit, since it does not
// run.
func (a *Approver) state(req ToolCallRequest) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "tool: %s\n", req.Name)
	if req.Name == "bash" {
		if _, ok := BashGrantPrefix(req.Arguments); !ok {
			return "", errCompound
		}
		var args struct {
			Script string `json:"script"`
		}
		_ = json.Unmarshal([]byte(req.Arguments), &args)
		fmt.Fprintf(&b, "command: %s\n", args.Script)
	} else {
		fmt.Fprintf(&b, "arguments: %s\n", req.Arguments)
	}
	if a.workDir != "" {
		fmt.Fprintf(&b, "working directory: %s\n", a.workDir)
	}
	if b.Len() > maxApproverState {
		return "", errTooLong
	}
	if req.Reasoning != "" {
		reason := "reason: " + req.Reasoning + "\n"
		if room := maxApproverState - b.Len(); len(reason) > room {
			reason = strings.ToValidUTF8(reason[:room], "")
		}
		b.WriteString(reason)
	}
	return b.String(), nil
}
