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
	"github.com/mudler/wiz/chat"
	"github.com/mudler/wiz/types"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
	colorRed    = "\033[31m"
	colorPurple = "\033[35m"
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
				fmt.Printf("\r%s%s %s%s", colorCyan, spinnerFrames[frame], msg, colorReset)
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
		return fmt.Sprintf("%s🤖 %s (%s) completed: %s%s", colorGray, typ, id, ev.Result, colorReset)
	case chat.AgentStatusFailed:
		return fmt.Sprintf("%s🤖 %s (%s) failed: %v%s", colorRed, typ, id, ev.Err, colorReset)
	default:
		return fmt.Sprintf("%s🤖 %s (%s) %s%s", colorGray, typ, id, ev.Status, colorReset)
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
			fmt.Printf("%s💭 %s%s\n", colorGray, reasoning, colorReset)
			spin.start("Conjuring...")
		},
		OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
			spin.stop()
			fmt.Println()
			fmt.Println(strings.Repeat("─", 50))
			fmt.Printf("%s%s🔧 Tool Request: %s%s\n", colorBold, colorYellow, req.Name, colorReset)
			fmt.Printf("%sArguments:%s %s\n", colorGray, colorReset, req.Arguments)
			if req.Reasoning != "" {
				fmt.Printf("%s💭 %s%s\n", colorGray, req.Reasoning, colorReset)
			}
			fmt.Println(strings.Repeat("─", 50))
			fmt.Printf("\n%s[y]es  [a]lways  [n]o  or type adjustment:%s ", colorCyan, colorReset)

			text, _ := readStringCancellable(ctx, reader)
			text = strings.TrimSpace(text)
			fmt.Println()

			var response chat.ToolCallResponse
			switch strings.ToLower(text) {
			case "y", "yes":
				response = chat.ToolCallResponse{Approved: true}
				spin.start("Executing tool...")
			case "a", "always":
				response = chat.ToolCallResponse{Approved: true, AlwaysAllow: true}
				fmt.Printf("%s✓ Tool '%s' added to allow list for this session%s\n", colorGreen, req.Name, colorReset)
				spin.start("Executing tool...")
			case "n", "no":
				response = chat.ToolCallResponse{Approved: false}
				fmt.Printf("%s✗ Tool execution denied%s\n", colorRed, colorReset)
			default:
				response = chat.ToolCallResponse{Approved: true, Adjustment: text}
				spin.start("Executing adjusted tool...")
			}
			return response
		},
		OnResponse: func(response string) {
			spin.stop()
			fmt.Println()
			fmt.Println(strings.Repeat("─", 50))
			fmt.Printf("%s%s🧙 Wiz:%s\n", colorBold, colorPurple, colorReset)
			fmt.Println(response)
			fmt.Println(strings.Repeat("─", 50))
		},
		OnPlan: func(plan chat.Plan) chat.PlanResponse {
			spin.stop()
			fmt.Println()
			fmt.Println(strings.Repeat("─", 50))
			fmt.Printf("%s%s📋 Plan%s\n", colorBold, colorPurple, colorReset)
			fmt.Println(strings.Repeat("─", 50))
			fmt.Printf("%sDescription:%s %s\n", colorGray, colorReset, plan.Description)
			if len(plan.Subtasks) > 0 {
				fmt.Printf("%sSubtasks:%s\n", colorGray, colorReset)
				for i, subtask := range plan.Subtasks {
					fmt.Printf("  %d. %s\n", i+1, subtask)
				}
			}
			fmt.Println(strings.Repeat("─", 50))
			fmt.Printf("\n%s[Enter] approve  [n]o:%s ", colorCyan, colorReset)

			text, _ := readStringCancellable(ctx, reader)
			text = strings.TrimSpace(strings.ToLower(text))
			fmt.Println()

			var response chat.PlanResponse
			if text == "n" || text == "no" {
				response = chat.PlanResponse{Approved: false}
				fmt.Printf("%s✗ Plan execution cancelled%s\n", colorRed, colorReset)
			} else {
				response = chat.PlanResponse{Approved: true}
				spin.start("Executing plan...")
			}
			return response
		},
		OnError: func(err error) {
			spin.stop()
			fmt.Fprintf(os.Stderr, "%s✗ Error: %v%s\n", colorRed, err, colorReset)
		},
		OnAgentEvent: func(ev chat.AgentEvent) {
			spin.stop()
			fmt.Println(formatAgentEventLine(ev))
			spin.start("Conjuring...")
		},
	}

	session, err := chat.NewSession(ctx, cfg, callbacks, transports...)
	if err != nil {
		return err
	}
	defer session.Close()

	fmt.Printf("%s%s✨ [◠ ◠] wiz%s\n", colorBold, colorPurple, colorReset)
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("%sYour terminal wizard awaits. Type your command and press Enter.%s\n", colorGray, colorReset)
	fmt.Printf("%sCtrl+C to exit. Type /plan to toggle plan mode.%s\n\n", colorGray, colorReset)

	// Display help immediately
	help()

	planMode := false
	session.SetPlanMode(planMode)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			modeIndicator := ""
			if planMode {
				modeIndicator = fmt.Sprintf(" %s[PLAN MODE]%s", colorYellow, colorReset)
			}
			fmt.Printf("%s>%s%s ", colorCyan, colorReset, modeIndicator)

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
				switch text {
				case "/plan":
					planMode = !planMode
					session.SetPlanMode(planMode)
					if planMode {
						fmt.Printf("%s✓ Plan mode enabled%s\n", colorGreen, colorReset)
					} else {
						fmt.Printf("%s✓ Plan mode disabled%s\n", colorGreen, colorReset)
					}
					continue
				default:
					fmt.Printf("%s✗ Unknown command: %s%s\n", colorRed, text, colorReset)
					fmt.Printf("%sType 'help' for available commands.%s\n", colorGray, colorReset)
					continue
				}
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
			spin.start("Casting spell...")
			_, err = session.SendMessage(text)
			spin.stop()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s✗ Error: %v%s\n", colorRed, err, colorReset)
			}
			fmt.Println()
		}
	}
}

func help() {
	fmt.Println("Available commands:")
	fmt.Println("  exit - Exit the wizard")
	fmt.Println("  help - Show this help message")
	fmt.Println("  clear - Clear the conversation")
	fmt.Println("  /plan - Toggle plan mode")
}
