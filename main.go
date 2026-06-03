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
	"github.com/mudler/xlog"
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

func main() {
	// Subcommand dispatch (must precede flag parsing).
	if len(os.Args) >= 2 && os.Args[1] == "plugin" {
		os.Exit(cmd.RunPluginCommand(os.Args[2:]))
	}
	if len(os.Args) >= 2 && os.Args[1] == "skill" {
		os.Exit(cmd.RunSkillCommand(os.Args[2:]))
	}

	// Parse command line arguments
	heightFlag := flag.String("height", "", "Height of the TUI (e.g., '40%' or '20')")
	initFlag := flag.String("init", "", "Output shell integration script (zsh, bash, or fish)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	tmuxFlag := flag.Bool("tmux", false, "Run in tmux popup (auto-detected if in tmux)")
	noTmuxFlag := flag.Bool("no-tmux", false, "Disable tmux popup even when in tmux")
	tuiFlag := flag.Bool("tui", false, "Start the full-screen TUI directly (no tmux popup)")
	cliFlag := flag.Bool("cli", false, "Run in plain CLI mode instead of the TUI")
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

	if cfg.LogLevel == "" {
		cfg.LogLevel = "error"
	}

	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(cfg.LogLevel), os.Getenv("LOG_FORMAT")))

	// Shared shell-job registry: the shell MCP server starts/manages jobs in it,
	// and the TUI lists them (footer) and backgrounds the foreground one (Ctrl+B).
	shellJobs := mcp.NewShellJobs()

	transports, err := mcp.StartTransports(ctx, cfg, shellJobs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting MCP servers: %v\n", err)
		os.Exit(1)
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
		if err := cmd.RunCLI(ctx, cfg, transports...); err != nil {
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
