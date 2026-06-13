package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

// resolveCLIInput maps a CLI input line to a slash Action, mirroring the TUI.
func resolveCLIInput(input string, cfg types.Config) slash.Action {
	return slash.Resolve(input, cfg.Commands, cfg.Skills, cfg.Agents)
}

// Spinner frames for animated display
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner manages an animated spinner for CLI output
type spinner struct {
	mu       sync.Mutex
	active   bool
	message  string
	stopChan chan struct{}
	doneChan chan struct{}
}

func newSpinner() *spinner {
	return &spinner{
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
	}
}

func (s *spinner) start(message string) {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.message = message
	s.stopChan = make(chan struct{})
	s.doneChan = make(chan struct{})
	s.mu.Unlock()

	go func() {
		frame := 0
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		defer close(s.doneChan)

		for {
			select {
			case <-s.stopChan:
				// Clear the spinner line
				fmt.Print("\r\033[K")
				return
			case <-ticker.C:
				s.mu.Lock()
				msg := s.message
				s.mu.Unlock()
				fmt.Printf("\r%s %s", theme.Help.Render(spinnerFrames[frame]), theme.Help.Render(msg))
				frame = (frame + 1) % len(spinnerFrames)
			}
		}
	}()
}

func (s *spinner) update(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = message
}

func (s *spinner) stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	s.mu.Unlock()

	close(s.stopChan)
	<-s.doneChan
}

// readStringCancellable reads a line from the reader, but can be cancelled via context
func readStringCancellable(ctx context.Context, reader *bufio.Reader) (string, error) {
	type result struct {
		text string
		err  error
	}
	resultChan := make(chan result, 1)

	go func() {
		text, err := reader.ReadString('\n')
		resultChan <- result{text: text, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-resultChan:
		return res.text, res.err
	}
}

// formatAgentEventLine renders a one-line CLI notification for a sub-agent event.
func formatAgentEventLine(ev chat.AgentEvent) string {
	id := ev.ID
	if len(id) > 8 {
		id = id[:8]
	}
	typ := ev.Type
	if typ == "" {
		typ = "agent"
	}
	switch ev.Status {
	case chat.AgentStatusCompleted:
		return theme.Subtle.Render(fmt.Sprintf("%s %s (%s) completed%s: %s", theme.SubAgent, typ, id, ev.StatsSuffix(), ev.Result))
	case chat.AgentStatusFailed:
		return theme.Error.Render(fmt.Sprintf("%s %s (%s) failed: %v", theme.SubAgent, typ, id, ev.Err))
	default:
		return theme.Subtle.Render(fmt.Sprintf("%s %s (%s) %s", theme.SubAgent, typ, id, ev.Status))
	}
}

func RunCLI(ctx context.Context, cfg types.Config, shellJobs *wizmcp.ShellJobs, transports ...mcp.Transport) error {
	reader := bufio.NewReader(os.Stdin)
	spin := newSpinner()

	callbacks := chat.Callbacks{
		OnStatus: func(status string) {
			spin.update(status)
		},
		OnReasoning: func(reasoning string) {
			spin.stop()
			fmt.Println(theme.ReasoningHeader())
			for _, line := range strings.Split(strings.TrimRight(reasoning, "\n"), "\n") {
				fmt.Println("  " + theme.Reasoning.Render(line))
			}
			spin.start(theme.Status(theme.VerbThinking, 0))
		},
		OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
			spin.stop()
			g := theme.Gutter.Render(theme.ApprovalGutter) + " "
			fmt.Println()
			fmt.Println(g + theme.ApproveKey.Render(req.Name+" wants to run"))
			for _, line := range strings.Split(chat.FormatToolCall(req.Name, req.Arguments), "\n") {
				fmt.Println(g + theme.Help.Render(line))
			}
			if req.Reasoning != "" {
				fmt.Println(g + theme.Reasoning.Render(req.Reasoning))
			}
			scope, prefix := chat.GrantScope(req.Name, req.Arguments)
			fmt.Print(g + theme.ApproveKey.Render(theme.CLIApprovePrompt(scope)) + " ")

			text, _ := readStringCancellable(ctx, reader)
			text = strings.TrimSpace(text)
			fmt.Println()

			var response chat.ToolCallResponse
			switch strings.ToLower(text) {
			case "y", "yes", "1":
				response = chat.ToolCallResponse{Approved: true}
				spin.start(theme.Status(theme.VerbWorking, 0))
			case "a", "always", "2":
				response = chat.ToolCallResponse{Approved: true, AlwaysAllow: true, AlwaysPrefix: prefix}
				if prefix != "" {
					fmt.Println(theme.Subtle.Render("allowing " + prefix + " … commands for this session"))
				} else {
					fmt.Println(theme.Subtle.Render("added '" + req.Name + "' to the session allow list"))
				}
				spin.start(theme.Status(theme.VerbWorking, 0))
			case "all", "3":
				response = chat.ToolCallResponse{Approved: true, AllowAllTurn: true}
				fmt.Println(theme.Subtle.Render("approving all tool calls for this turn"))
				spin.start(theme.Status(theme.VerbWorking, 0))
			case "n", "no":
				response = chat.ToolCallResponse{Approved: false}
				fmt.Println(theme.Error.Render(theme.Cross + " denied"))
			default:
				response = chat.ToolCallResponse{Approved: true, Adjustment: text}
				spin.start(theme.Status(theme.VerbWorking, 0))
			}
			return response
		},
		OnResponse: func(response string) {
			spin.stop()
			fmt.Println()
			fmt.Println(theme.LabelNib.Render(theme.BrandName) + " " + theme.SepStyle.Render(theme.Sep))
			fmt.Println(response)
			fmt.Println()
		},
		OnCompactDone: func(before, after int) {
			fmt.Println(theme.Subtle.Render(compactNotice(before, after)))
		},
		OnError: func(err error) {
			spin.stop()
			fmt.Fprintln(os.Stderr, theme.Error.Render(theme.Cross+" "+err.Error()))
		},
		OnToolResult: func(res chat.ToolResult) {
			preview := chat.PreviewResult(res.Result, 12)
			if preview == "" {
				return
			}
			label := res.Name
			if res.AgentID != "" {
				id := res.AgentID
				if len(id) > 8 {
					id = id[:8]
				}
				label = theme.SubAgent + " " + id + " · " + res.Name
			}
			spin.stop()
			fmt.Println(theme.Subtle.Render(theme.Sep + " " + label))
			for _, line := range strings.Split(preview, "\n") {
				fmt.Println(theme.Help.Render("  " + line))
			}
			spin.start(theme.Status(theme.VerbThinking, 0))
		},
		OnAgentEvent: func(ev chat.AgentEvent) {
			spin.stop()
			fmt.Println(formatAgentEventLine(ev))
			spin.start(theme.Status(theme.VerbThinking, 0))
		},
	}

	session, err := chat.NewSession(ctx, cfg, callbacks, transports...)
	if err != nil {
		return err
	}
	defer session.Close()
	if shellJobs != nil {
		// Keep a run parked while a background shell job is still running and
		// inject its completion notice, so bash_background work isn't orphaned.
		session.SetShellJobs(shellJobs)
	}

	fmt.Println(theme.Brand.Render(theme.BrandName))
	fmt.Println(theme.Rule.Render(strings.Repeat("─", 50)))
	fmt.Println(theme.Help.Render(theme.CLIWelcome))
	fmt.Println(theme.Help.Render(theme.CLIExit))
	fmt.Println()

	// Display help immediately
	help()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			fmt.Print(theme.Prompt.Render(theme.PromptGlyph) + " ")

			text, err := readStringCancellable(ctx, reader)
			if err != nil {
				return err
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}

			switch text {
			case "clear":
				session.ClearHistory()
				continue
			case "exit":
				return nil
			case "help":
				help()
				continue
			}

			action := resolveCLIInput(text, cfg)
			switch action.Kind {
			case slash.KindError:
				fmt.Fprintln(os.Stderr, theme.Error.Render(theme.Cross+" "+action.Err))
				continue
			case slash.KindLoadSkill:
				notice, err := session.LoadSkill(action.Skill)
				if err != nil {
					fmt.Fprintln(os.Stderr, theme.Error.Render(theme.Cross+" "+err.Error()))
				} else {
					fmt.Println(theme.Subtle.Render(notice))
				}
				continue
			case slash.KindCompact:
				spin.start(theme.Status(theme.VerbThinking, 0))
				before, after, err := session.CompactHistory()
				spin.stop()
				if err != nil {
					fmt.Fprintln(os.Stderr, theme.Error.Render(theme.Cross+" "+err.Error()))
				} else if before == after {
					fmt.Println(theme.Subtle.Render("Nothing to compact yet."))
				} else {
					fmt.Println(theme.Subtle.Render(compactNotice(before, after)))
				}
				continue
			default: // slash.KindSend
				fmt.Println()
				spin.start(theme.Status(theme.VerbThinking, 0))
				_, err = session.SendMessage(action.Text)
				spin.stop()
				if err != nil {
					fmt.Fprintln(os.Stderr, theme.Error.Render(theme.Cross+" "+err.Error()))
				}
				fmt.Println()
			}
		}
	}
}

// compactNotice formats the one-line summary shown after a conversation is compacted.
func compactNotice(before, after int) string {
	return fmt.Sprintf("📦 Compacted conversation — %s → %s tokens", chat.HumanTokens(before), chat.HumanTokens(after))
}

func help() {
	fmt.Println(theme.Help.Render("commands:  exit  ·  clear  ·  help"))
}
