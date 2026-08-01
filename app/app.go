// Package app is nib's entrypoint, importable so nib can be embedded in
// another binary. nib's own main.go is a thin wrapper over Main.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/mudler/nib/cmd"
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/internal"
	"github.com/mudler/nib/mcp"
	"github.com/mudler/nib/setup"
	"github.com/mudler/xlog"
	"golang.org/x/term"
)

// Options configures a nib invocation. The zero value reproduces standalone
// nib's behavior exactly, so embedders opt in to each difference.
type Options struct {
	// Args are the arguments after the program name (os.Args[1:]).
	Args []string
	// ProgramName is the name shown in usage and error messages. Empty means "nib".
	ProgramName string
	// BaseDir overrides the config, plugins, and skills root. Empty means
	// nib's default XDG resolution.
	BaseDir string
	// Stdin, Stdout, Stderr default to the process streams when nil.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (o Options) name() string {
	if o.ProgramName == "" {
		return "nib"
	}
	return o.ProgramName
}

func (o Options) stdin() io.Reader {
	if o.Stdin == nil {
		return os.Stdin
	}
	return o.Stdin
}

func (o Options) stdout() io.Writer {
	if o.Stdout == nil {
		return os.Stdout
	}
	return o.Stdout
}

func (o Options) stderr() io.Writer {
	if o.Stderr == nil {
		return os.Stderr
	}
	return o.Stderr
}

// Main runs nib and returns a process exit code. argv is the full argument
// vector including the program name.
func Main(argv []string) int {
	var args []string
	if len(argv) > 1 {
		args = argv[1:]
	}
	return run(Options{Args: args})
}

// Run runs nib and returns an error. It never calls os.Exit. A non-zero exit
// code from a management subcommand is returned as an ExitError.
func Run(ctx context.Context, o Options) error {
	if code := runCtx(ctx, o); code != 0 {
		return ExitError{Code: code}
	}
	return nil
}

// ExitError carries a non-zero exit code out of Run.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

func run(o Options) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// run is library code, so the handler has to be torn down on the way out:
	// without signal.Stop the channel stays registered with the runtime, and
	// without the ctx.Done() arm the goroutine blocks on <-sigs forever.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			cancel()
		case <-ctx.Done():
		}
	}()

	return runCtx(ctx, o)
}

func runCtx(ctx context.Context, o Options) int {
	args := o.Args

	// Subcommand dispatch must precede flag parsing.
	if len(args) >= 1 && args[0] == "plugin" {
		return cmd.RunPluginCommand(o.BaseDir, args[1:])
	}
	if len(args) >= 1 && args[0] == "skill" {
		return cmd.RunSkillCommand(o.BaseDir, args[1:])
	}
	// `nib mcp <add|list|remove|test>` manages configured servers and early-exits
	// (needs config, not transports). Bare `nib mcp` / --http / --stdio still serve.
	if len(args) >= 2 && args[0] == "mcp" && cmd.IsMCPManageSubcommand(args[1]) {
		return cmd.RunMCPCommand(o.BaseDir, args[1:])
	}

	// `nib mcp` needs config + transports (built below), so it cannot early-exit
	// like plugin/skill. Capture its args and hide them from the flag parser.
	mcpMode := len(args) >= 1 && args[0] == "mcp"
	var mcpArgs []string
	if mcpMode {
		mcpArgs = args[1:]
		args = nil
	}

	fs := flag.NewFlagSet(o.name(), flag.ContinueOnError)
	fs.SetOutput(o.stderr())
	heightFlag := fs.String("height", "", "Height of the TUI (e.g., '40%' or '20')")
	initFlag := fs.String("init", "", "Output shell integration script (zsh, bash, or fish)")
	versionFlag := fs.Bool("version", false, "Print version and exit")
	tmuxFlag := fs.Bool("tmux", false, "Run in tmux popup (auto-detected if in tmux)")
	noTmuxFlag := fs.Bool("no-tmux", false, "Disable tmux popup even when in tmux")
	tuiFlag := fs.Bool("tui", false, "Start the full-screen TUI directly (no tmux popup)")
	cliFlag := fs.Bool("cli", false, "Run in plain CLI mode instead of the TUI")
	setupFlag := fs.Bool("setup", false, "Run the interactive model setup wizard")
	traceDirFlag := fs.String("trace-dir", "", "Write a session LLM trace (NDJSON) to this directory; also via NIB_TRACE_DIR")
	yoloFlag := fs.Bool("yolo", false, "Auto-approve every tool call without prompting; also via NIB_YOLO")
	if err := fs.Parse(args); err != nil {
		// ContinueOnError hands back ErrHelp for -h/--help, which the global
		// flag.CommandLine used to turn into a clean exit. Keep that.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *versionFlag {
		fmt.Fprintf(o.stdout(), "%s %s\n", o.name(), internal.PrintableVersion())
		return 0
	}

	if *initFlag != "" {
		script := cmd.GetInitScript(*initFlag)
		if script == "" {
			fmt.Fprintf(o.stderr(), "Unknown shell: %s. Supported: zsh, bash, fish\n", *initFlag)
			return 1
		}
		fmt.Fprint(o.stdout(), script)
		return 0
	}

	cfg := config.LoadWith(config.LoadOptions{BaseDir: o.BaseDir})

	// Tracing is runtime-only: the flag wins, otherwise fall back to the env var.
	if *traceDirFlag != "" {
		cfg.TraceDir = *traceDirFlag
	} else if env := os.Getenv("NIB_TRACE_DIR"); env != "" {
		cfg.TraceDir = env
	}

	// "yolo" mode auto-approves every tool call. The flag or env var force
	// "auto" approval, overriding whatever the config file set.
	if *yoloFlag || envTrue(os.Getenv("NIB_YOLO")) {
		cfg.ApprovalMode = "auto"
	}

	if cfg.LogLevel == "" {
		cfg.LogLevel = "error"
	}
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(cfg.LogLevel), os.Getenv("LOG_FORMAT")))

	isTTY := false
	if f, ok := o.stdin().(*os.File); ok {
		isTTY = term.IsTerminal(int(f.Fd()))
	}

	switch decideSetup(cfg.Model != "", *setupFlag, isTTY) {
	case setupAbort:
		if *setupFlag {
			fmt.Fprintf(o.stderr(), "%s --setup requires an interactive terminal\n", o.name())
		} else {
			fmt.Fprintf(o.stderr(), "%s: no model configured. Run `%s --setup`, or set MODEL/API_KEY/BASE_URL.\n", o.name(), o.name())
		}
		return 1
	case setupRun:
		newCfg, saved, err := setup.Run(ctx, cfg)
		if err != nil {
			fmt.Fprintf(o.stderr(), "setup: %v\n", err)
			return 1
		}
		if !saved {
			return 0 // user cancelled
		}
		cfg.Model, cfg.APIKey, cfg.BaseURL = newCfg.Model, newCfg.APIKey, newCfg.BaseURL
	}

	// Shared shell-job registry: the shell MCP server starts/manages jobs in it,
	// and the TUI lists them (footer) and backgrounds the foreground one (Ctrl+B).
	shellJobs := mcp.NewShellJobs()

	transports, err := mcp.StartTransports(ctx, cfg, shellJobs)
	if err != nil {
		fmt.Fprintf(o.stderr(), "Error starting MCP servers: %v\n", err)
		return 1
	}

	if mcpMode {
		if err := cmd.RunMCP(ctx, cfg, mcpArgs, shellJobs, transports...); err != nil {
			fmt.Fprintf(o.stderr(), "Error: %v\n", err)
			return 1
		}
		return 0
	}

	mode := selectMode(modeInputs{
		cli:    *cliFlag,
		tui:    *tuiFlag,
		tmux:   *tmuxFlag,
		height: *heightFlag,
		inTmux: cmd.IsInTmux(),
	})

	switch mode {
	case modeCLI:
		if err := cmd.RunCLI(ctx, cfg, shellJobs, transports...); err != nil {
			fmt.Fprintf(o.stderr(), "Error: %v\n", err)
			return 1
		}
	case modeInline:
		h := *heightFlag
		if h == "" {
			h = "40%"
		}
		height := parseHeight(h)
		useTmux := *tmuxFlag || (cmd.IsInTmux() && !*noTmuxFlag)
		if useTmux && cmd.IsInTmux() {
			if err := cmd.RunTmuxSplit(*heightFlag); err != nil {
				fmt.Fprintf(o.stderr(), "Error: %v\n", err)
				return 1
			}
		} else {
			if err := cmd.RunTUI(ctx, cfg, height, shellJobs, transports...); err != nil {
				fmt.Fprintf(o.stderr(), "Error: %v\n", err)
				return 1
			}
		}
	default: // modeTUI, fullscreen, direct (no tmux split)
		if err := cmd.RunTUI(ctx, cfg, parseHeight("100%"), shellJobs, transports...); err != nil {
			fmt.Fprintf(o.stderr(), "Error: %v\n", err)
			return 1
		}
	}
	return 0
}

// parseHeight parses a height string like "40%" or "20". A negative result
// means "percentage of terminal height".
func parseHeight(s string) int {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		pct, err := strconv.Atoi(strings.TrimSuffix(s, "%"))
		if err != nil || pct <= 0 || pct > 100 {
			return 40 // default
		}
		return -pct // negative means percentage
	}
	height, err := strconv.Atoi(s)
	if err != nil || height <= 0 {
		return 20 // default
	}
	return height
}

// envTrue reports whether an environment variable value is truthy. Empty,
// "0", "false", "no", and "off" (any case) are false; everything else is true,
// so `NIB_YOLO=1`, `NIB_YOLO=true`, and a bare `NIB_YOLO=` set in the shell all
// behave sensibly.
func envTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}
