package main

// setupDecision is the outcome of the first-run setup gate.
type setupDecision int

const (
	setupSkip  setupDecision = iota // a model is configured; proceed normally
	setupRun                        // launch the interactive wizard
	setupAbort                      // can't run interactively; print hint and exit
)

// decideSetup decides whether to run the model setup wizard. forced is the
// --setup flag; isTTY reports whether stdin is an interactive terminal.
func decideSetup(modelConfigured, forced, isTTY bool) setupDecision {
	if !forced && modelConfigured {
		return setupSkip
	}
	if !isTTY {
		return setupAbort
	}
	return setupRun
}
