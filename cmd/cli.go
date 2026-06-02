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
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

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
		return theme.Subtle.Render(fmt.Sprintf("%s %s (%s) completed: %s", theme.SubAgent, typ, id, ev.Result))
	case chat.AgentStatusFailed:
		return theme.Error.Render(fmt.Sprintf("%s %s (%s) failed: %v", theme.SubAgent, typ, id, ev.Err))
	default:
		return theme.Subtle.Render(fmt.Sprintf("%s %s (%s) %s", theme.SubAgent, typ, id, ev.Status))
	}
}

func RunCLI(ctx context.Context, cfg types.Config, transports ...mcp.Transport) error {
	reader := bufio.NewReader(os.Stdin)
	spin := newSpinner()

	callbacks := chat.Callbacks{
		OnStatus: func(status string) {
			spin.update(status)
		},
		OnReasoning: func(reasoning string) {
			spin.stop()
			fmt.Println(theme.Reasoning.Render(reasoning))
			spin.start(theme.Status(theme.VerbThinking, 0))
		},
		OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
			spin.stop()
			g := theme.Gutter.Render(theme.ApprovalGutter) + " "
			fmt.Println()
			fmt.Println(g + theme.ApproveKey.Render("run  "+req.Name))
			fmt.Println(g + theme.Help.Render(req.Arguments))
			if req.Reasoning != "" {
				fmt.Println(g + theme.Reasoning.Render(req.Reasoning))
			}
			fmt.Print(g + theme.ApproveKey.Render(theme.ApprovePrompt) + " ")

			text, _ := readStringCancellable(ctx, reader)
			text = strings.TrimSpace(text)
			fmt.Println()

			var response chat.ToolCallResponse
			switch strings.ToLower(text) {
			case "y", "yes":
				response = chat.ToolCallResponse{Approved: true}
				spin.start(theme.Status(theme.VerbWorking, 0))
			case "a", "always":
				response = chat.ToolCallResponse{Approved: true, AlwaysAllow: true}
				fmt.Println(theme.Subtle.Render("added '" + req.Name + "' to the session allow list"))
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
		OnError: func(err error) {
			spin.stop()
			fmt.Fprintln(os.Stderr, theme.Error.Render(theme.Cross+" "+err.Error()))
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

			// Handle commands starting with /
			if strings.HasPrefix(text, "/") {
				fmt.Println(theme.Error.Render(theme.Cross + " unknown command: " + text))
				fmt.Println(theme.Help.Render("type 'help' for available commands"))
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

			fmt.Println()
			spin.start(theme.Status(theme.VerbThinking, 0))
			_, err = session.SendMessage(text)
			spin.stop()
			if err != nil {
				fmt.Fprintln(os.Stderr, theme.Error.Render(theme.Cross+" "+err.Error()))
			}
			fmt.Println()
		}
	}
}

func help() {
	fmt.Println(theme.Help.Render("commands:  exit  ·  clear  ·  help"))
}
