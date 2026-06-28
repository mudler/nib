package main

import (
	"context"
	"flag"
	"fmt"
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

// parseHeight parses a height string like "40%" or "20"
func parseHeight(s string) int {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		// Percentage of terminal height
		pct, err := strconv.Atoi(strings.TrimSuffix(s, "%"))
		if err != nil || pct <= 0 || pct > 100 {
			return 40 // default
		}
		// We'll calculate actual height in the TUI based on terminal size
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

func main() {
	// Subcommand dispatch (must precede flag parsing).
	if len(os.Args) >= 2 && os.Args[1] == "plugin" {
		os.Exit(cmd.RunPluginCommand(os.Args[2:]))
	}
	if len(os.Args) >= 2 && os.Args[1] == "skill" {
		os.Exit(cmd.RunSkillCommand(os.Args[2:]))
	}

	// `nib mcp <add|list|remove|test>` manages configured servers and early-exits
	// (needs config, not transports). Bare `nib mcp` / --http / --stdio still serve.
	if len(os.Args) >= 3 && os.Args[1] == "mcp" && cmd.IsMCPManageSubcommand(os.Args[2]) {
		os.Exit(cmd.RunMCPCommand(os.Args[2:]))
	}

	// `nib mcp` needs config + transports (built below), so it cannot early-exit
	// like plugin/skill. Capture its args and hide them from the global flag
	// parser; the actual branch is after StartTransports.
	mcpMode := len(os.Args) >= 2 && os.Args[1] == "mcp"
	var mcpArgs []string
	if mcpMode {
		mcpArgs = os.Args[2:]
		os.Args = os.Args[:1]
	}

	// Parse command line arguments
	heightFlag := flag.String("height", "", "Height of the TUI (e.g., '40%' or '20')")
	initFlag := flag.String("init", "", "Output shell integration script (zsh, bash, or fish)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	tmuxFlag := flag.Bool("tmux", false, "Run in tmux popup (auto-detected if in tmux)")
	noTmuxFlag := flag.Bool("no-tmux", false, "Disable tmux popup even when in tmux")
	tuiFlag := flag.Bool("tui", false, "Start the full-screen TUI directly (no tmux popup)")
	cliFlag := flag.Bool("cli", false, "Run in plain CLI mode instead of the TUI")
	setupFlag := flag.Bool("setup", false, "Run the interactive model setup wizard")
	traceDirFlag := flag.String("trace-dir", "", "Write a session LLM trace (NDJSON) to this directory; also via NIB_TRACE_DIR")
	yoloFlag := flag.Bool("yolo", false, "Auto-approve every tool call without prompting; also via NIB_YOLO")
	flag.Parse()

	// Handle version flag
	if *versionFlag {
		fmt.Printf("nib %s\n", internal.PrintableVersion())
		os.Exit(0)
	}

	// Handle init command
	if *initFlag != "" {
		script := cmd.GetInitScript(*initFlag)
		if script == "" {
			fmt.Fprintf(os.Stderr, "Unknown shell: %s. Supported: zsh, bash, fish\n", *initFlag)
			os.Exit(1)
		}
		fmt.Print(script)
		os.Exit(0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		cancel()
	}()

	cfg := config.Load()

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

	switch decideSetup(cfg.Model != "", *setupFlag, term.IsTerminal(int(os.Stdin.Fd()))) {
	case setupAbort:
		if *setupFlag {
			fmt.Fprintln(os.Stderr, "nib --setup requires an interactive terminal")
		} else {
			fmt.Fprintln(os.Stderr, "nib: no model configured. Run `nib --setup`, or set MODEL/API_KEY/BASE_URL.")
		}
		os.Exit(1)
	case setupRun:
		newCfg, saved, err := setup.Run(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "setup: %v\n", err)
			os.Exit(1)
		}
		if !saved {
			os.Exit(0) // user cancelled
		}
		cfg.Model, cfg.APIKey, cfg.BaseURL = newCfg.Model, newCfg.APIKey, newCfg.BaseURL
	}

	// Shared shell-job registry: the shell MCP server starts/manages jobs in it,
	// and the TUI lists them (footer) and backgrounds the foreground one (Ctrl+B).
	shellJobs := mcp.NewShellJobs()

	transports, err := mcp.StartTransports(ctx, cfg, shellJobs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting MCP servers: %v\n", err)
		os.Exit(1)
	}

	if mcpMode {
		if err := cmd.RunMCP(ctx, cfg, mcpArgs, shellJobs, transports...); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Determine mode based on flags. Bare invocation -> fullscreen TUI (default);
	// --cli -> plain CLI; --height/--tmux -> inline/tmux drop-down widget.
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
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
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
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			if err := cmd.RunTUI(ctx, cfg, height, shellJobs, transports...); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	default: // modeTUI — fullscreen, direct (no tmux split)
		if err := cmd.RunTUI(ctx, cfg, parseHeight("100%"), shellJobs, transports...); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}
