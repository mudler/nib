package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

// unreachableCfg points at a port nothing listens on, so a resolved action
// that gets mis-dispatched to the model (KindSend) fails fast and locally —
// no DNS lookup, no dependence on outbound network access from the sandbox —
// instead of hanging or silently succeeding against a real endpoint.
var unreachableCfg = types.Config{Model: "test-model", BaseURL: "http://127.0.0.1:1"}

// This is the RED test for Task 21: before the fix, /yolo resolves to
// slash.KindYolo, cmd/cli.go has no case for it, and it falls into the
// default arm, which treats every unhandled Kind as KindSend — the literal
// text "/yolo" is handed to session.SendMessage and dispatched at the model.
// That must not happen: /yolo is a session-wide flag with a well-defined CLI
// meaning (chat.Session.SetAutoApprove), and toggling it must never touch the
// network.
func TestCLIYoloTogglesInsteadOfSendingToModel(t *testing.T) {
	out, errOut := runCLIScript(t, unreachableCfg, "/yolo\nexit\n")

	if !strings.Contains(out, theme.YoloOn) {
		t.Fatalf("expected the yolo-on notice in stdout, got %q", out)
	}
	if errOut != "" {
		t.Fatalf("yolo must not reach the model (no network attempt): stderr %q", errOut)
	}
}

// /yolo off and explicit /yolo on must resolve the same way.
func TestCLIYoloExplicitOnOff(t *testing.T) {
	out, _ := runCLIScript(t, unreachableCfg, "/yolo on\n/yolo off\nexit\n")

	if !strings.Contains(out, theme.YoloOn) {
		t.Fatalf("expected yolo-on notice, got %q", out)
	}
	if !strings.Contains(out, theme.YoloOff) {
		t.Fatalf("expected yolo-off notice, got %q", out)
	}
}

// KindResume has no CLI meaning (there is no picker surface in the plain
// REPL); it must be reported as unavailable, not sent to the model as chat
// text.
func TestCLIResumeReportsNotAvailable(t *testing.T) {
	out, errOut := runCLIScript(t, unreachableCfg, "/resume\nexit\n")

	if !strings.Contains(out, "not available in CLI mode") {
		t.Fatalf("expected a not-available notice, got stdout %q", out)
	}
	if errOut != "" {
		t.Fatalf("/resume must not reach the model: stderr %q", errOut)
	}
}

// The unhandled-Kind default must not be special-cased to just KindYolo and
// KindResume: any other Kind without an explicit case (KindGoalShow here,
// standing in for the whole /goal and /loop family) must also be refused
// rather than silently sent to the model. This is the actual deliverable of
// Task 21 — the shape of the fallback, not just the two named instances.
func TestCLIUnhandledKindDefaultsToNotAvailable(t *testing.T) {
	out, errOut := runCLIScript(t, unreachableCfg, "/goal\nexit\n")

	if !strings.Contains(out, "not available in CLI mode") {
		t.Fatalf("expected a not-available notice for /goal, got stdout %q", out)
	}
	if errOut != "" {
		t.Fatalf("/goal must not reach the model: stderr %q", errOut)
	}
}

// /approve sets the approval mode in the REPL instead of reaching the model.
func TestCLIApproveSetsMode(t *testing.T) {
	out, errOut := runCLIScript(t, unreachableCfg, "/approve auto\n/approve\nexit\n")
	if !strings.Contains(out, fmt.Sprintf(theme.ApproveModeNotice, "auto")) {
		t.Fatalf("expected the mode notice, got %q", out)
	}
	if errOut != "" {
		t.Fatalf("/approve must not reach the model: stderr %q", errOut)
	}
}

// Without a classifier, /approve classify is refused with a reason.
func TestCLIApproveClassifyNeedsClassifier(t *testing.T) {
	out, _ := runCLIScript(t, unreachableCfg, "/approve classify\nexit\n")
	if !strings.Contains(out, "classifier") {
		t.Fatalf("expected a refusal naming the classifier, got %q", out)
	}
}

// /classifier sets, shows and removes the classifier in the REPL.
func TestCLIClassifierSetsAndShows(t *testing.T) {
	out, errOut := runCLIScript(t, unreachableCfg, "/classifier\n/classifier config gliner\n/classifier\n/classifier off\nexit\n")
	for _, want := range []string{theme.ClassifierNone, "gliner @ ", theme.ClassifierOffNotice} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %q", want, out)
		}
	}
	if errOut != "" {
		t.Fatalf("/classifier must not reach the model: stderr %q", errOut)
	}
}
