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

	// Parse command line arguments
	heightFlag := flag.String("height", "", "Height of the TUI (e.g., '40%' or '20')")
	initFlag := flag.String("init", "", "Output shell integration script (zsh, bash, or fish)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	tmuxFlag := flag.Bool("tmux", false, "Run in tmux popup (auto-detected if in tmux)")
	noTmuxFlag := flag.Bool("no-tmux", false, "Disable tmux popup even when in tmux")
	tuiFlag := flag.Bool("tui", false, "Start the full-screen TUI directly (no tmux popup)")
	flag.Parse()

	// Handle version flag
	if *versionFlag {
		fmt.Printf("wiz %s\n", internal.PrintableVersion())
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

	// Determine mode based on flags. --tui (or --height) selects the TUI; bare
	// `wiz` stays in CLI mode.
	if *tuiFlag || *heightFlag != "" {
		h := *heightFlag
		if h == "" {
			h = "40%" // sensible default when only --tui is given
		}
		height := parseHeight(h)

		// --tui forces a direct (non-tmux) TUI; otherwise honor tmux detection.
		useTmux := !*tuiFlag && (*tmuxFlag || (cmd.IsInTmux() && !*noTmuxFlag))

		if useTmux && cmd.IsInTmux() {
			// Run in tmux split pane (like fzf-tmux -d)
			if err := cmd.RunTmuxSplit(*heightFlag); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			// TUI mode
			if err := cmd.RunTUI(ctx, cfg, height, shellJobs, transports...); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		// CLI mode (original behavior)
		if err := cmd.RunCLI(ctx, cfg, transports...); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}
