package main

// runMode is the resolved execution mode for an invocation.
type runMode int

const (
	modeTUI    runMode = iota // fullscreen TUI (default)
	modeCLI                   // plain CLI (--cli)
	modeInline                // inline / tmux drop-down (--height / --tmux)
)

// modeInputs is the flag/environment state that decides the mode.
type modeInputs struct {
	cli    bool
	tui    bool
	tmux   bool
	height string
	inTmux bool
}

// selectMode resolves the run mode from flags. Precedence:
//  1. --cli  -> CLI
//  2. --height or --tmux -> inline/tmux drop-down (the shell widget path)
//  3. otherwise -> fullscreen TUI (the new default; --tui is an explicit alias)
//
// Note: bare invocation inside tmux still resolves to a direct fullscreen TUI;
// auto-split is reserved for the explicit --height widget path.
func selectMode(in modeInputs) runMode {
	switch {
	case in.cli:
		return modeCLI
	case in.height != "" || in.tmux:
		return modeInline
	default:
		return modeTUI
	}
}
