package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/attachments"
	"github.com/mudler/nib/attachstage"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

// ErrApprovalNoInput ends a CLI session that had to refuse a tool call because
// stdin was closed and nothing could approve it.
//
// It exists to be told apart from a session that simply ran out of input, which
// is a success. Under the piped one-shot idiom "I answered you" and "I refused
// to act" would otherwise both be exit 0 with stdout discarded, which is the
// same false signal, pointing the other way, as the EOF that used to be a
// failure. app maps it to its own exit code.
var ErrApprovalNoInput = errors.New("tool call denied: stdin closed, nothing could approve it")

// resolveCLIInput maps a CLI input line to a slash Action, mirroring the TUI.
func resolveCLIInput(input string, cfg types.Config) slash.Action {
	return slash.Resolve(input, cfg.Commands, cfg.Skills, cfg.Agents)
}

// Spinner frames for animated display
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner manages an animated spinner for CLI output.
//
// The animation uses a carriage return to redraw a single line in place, which
// only makes sense on an interactive terminal. When stdout is not a TTY (piped
// output, CI logs like GitHub Actions), the redraw is meaningless: every frame
// lands on its own line and the log fills with hundreds of "⠋ thinking"
// entries. In that case we fall back to a static, line-based status log that
// prints each distinct message once.
type spinner struct {
	mu       sync.Mutex
	out      io.Writer
	active   bool
	message  string
	stopChan chan struct{}
	doneChan chan struct{}
	tty      bool
	lastLine string // last message printed in non-TTY mode, for de-duplication
}

func newSpinner(out io.Writer) *spinner {
	return &spinner{
		out:      out,
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
		tty:      isTerminal(out),
	}
}

// printStatic emits a status line once in non-TTY mode, skipping consecutive
// duplicates so a steady "thinking" state produces a single line, not a flood.
// Caller must hold s.mu.
func (s *spinner) printStatic(message string) {
	if message == "" || message == s.lastLine {
		return
	}
	s.lastLine = message
	fmt.Fprintln(s.out, theme.Help.Render(message))
}

func (s *spinner) start(message string) {
	if !s.tty {
		s.mu.Lock()
		s.active = true
		s.message = message
		s.printStatic(message)
		s.mu.Unlock()
		return
	}

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
				fmt.Fprint(s.out, "\r\033[K")
				return
			case <-ticker.C:
				s.mu.Lock()
				msg := s.message
				s.mu.Unlock()
				fmt.Fprintf(s.out, "\r%s %s", theme.Help.Render(spinnerFrames[frame]), theme.Help.Render(msg))
				frame = (frame + 1) % len(spinnerFrames)
			}
		}
	}()
}

func (s *spinner) update(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = message
	if !s.tty {
		s.printStatic(message)
	}
}

func (s *spinner) stop() {
	if !s.tty {
		s.mu.Lock()
		s.active = false
		// Reset so the next start() reprints the status even if it repeats a
		// prior message, keeping the log readable across tool-call boundaries.
		s.lastLine = ""
		s.mu.Unlock()
		return
	}

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

// pause clears a live spinner so a mid-run notice can be printed on a clean
// line, and returns the func that puts the spinner back exactly as it was.
//
// The stop()/start(verb) pair the older callbacks use is wrong for a notice
// that arrives unbidden, for two reasons. It restarts a spinner that may never
// have been running — compaction fires after OnResponse has already stopped
// one, and a start() there leaves a spinner animating over the next prompt
// forever. And it stomps the verb: a callback that knows why it interrupted
// (a tool result means thinking resumes) may name the verb, but a notice that
// merely reports what the session did to itself has no business changing what
// the user was told the session is busy with.
//
// In non-TTY mode nothing is drawn in place, so there is no half-drawn line to
// clear; pausing there would only make the next start() reprint the status
// after every notice. Hence the early no-op.
func (s *spinner) pause() (resume func()) {
	if !s.tty {
		return func() {}
	}
	s.mu.Lock()
	active, msg := s.active, s.message
	s.mu.Unlock()
	if !active {
		return func() {}
	}
	s.stop()
	return func() { s.start(msg) }
}

// writeNotice prints a one-line notice that arrives mid-run, on a line of its
// own, without disturbing the spinner it interrupts.
func writeNotice(out io.Writer, spin *spinner, line string) {
	resume := spin.pause()
	fmt.Fprintln(out, line)
	resume()
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

func RunCLI(ctx context.Context, cfg types.Config, streams Streams, shellJobs *wizmcp.ShellJobs, transports ...mcp.Transport) error {
	in, out, errOut := streams.stdin(), streams.stdout(), streams.stderr()
	reader := bufio.NewReader(in)
	spin := newSpinner(out)

	// stdinClosed records that a read hit EOF. Both readers of this session
	// consult it: the prompt loop, to stop, and the approval callback, to deny
	// rather than ask a stream that cannot answer.
	//
	// deniedNoInput records that the denial actually happened, which is what
	// separates "the question was answered and the input ran out", a success,
	// from "something wanted approval and there was nobody to give it", which
	// has to be visible to a script that reads only the exit code.
	//
	// Both are atomic because the callbacks run on the agent's goroutine, not
	// the loop's.
	var stdinClosed, deniedNoInput atomic.Bool

	callbacks := chat.Callbacks{
		OnStatus: func(status string) {
			spin.update(status)
		},
		OnReasoning: func(reasoning string) {
			spin.stop()
			fmt.Fprintln(out, theme.ReasoningHeader())
			for _, line := range strings.Split(strings.TrimRight(reasoning, "\n"), "\n") {
				fmt.Fprintln(out, "  "+theme.Reasoning.Render(line))
			}
			spin.start(theme.Status(theme.VerbThinking, 0))
		},
		OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
			spin.stop()
			g := theme.Gutter.Render(theme.ApprovalGutter) + " "
			fmt.Fprintln(out)
			fmt.Fprintln(out, g+theme.ApproveKey.Render(req.Name+" wants to run"))
			for _, line := range strings.Split(chat.FormatToolCall(req.Name, req.Arguments), "\n") {
				fmt.Fprintln(out, g+theme.Help.Render(line))
			}
			if req.Reasoning != "" {
				fmt.Fprintln(out, g+theme.Reasoning.Render(req.Reasoning))
			}
			// Nothing can answer a prompt once stdin has closed, so record the
			// call and deny it instead of printing a question at a dead stream.
			if stdinClosed.Load() {
				deniedNoInput.Store(true)
				fmt.Fprintln(out, theme.Error.Render(theme.Cross+" "+theme.CLIDeniedNoInput))
				return chat.ToolCallResponse{Approved: false}
			}

			scope, prefix := chat.GrantScope(req.Name, req.Arguments)
			fmt.Fprint(out, g+theme.ApproveKey.Render(theme.CLIApprovePrompt(scope))+" ")

			text, readErr := readStringCancellable(ctx, reader)
			text = strings.TrimSpace(text)
			fmt.Fprintln(out)

			// A failed read is not a decision, and the switch below has no arm
			// that means "nobody answered": its default is the free-text
			// "approve, but do it like this" arm, which is right for a human
			// typing and catastrophic for an empty string handed back by a
			// closed stdin. Under the piped one-shot idiom that approved and
			// ran a shell command unattended, then exited 0. Fail closed.
			//
			// This is EOF and cancellation only. The empty line a human types
			// at a live terminal is a different thing, a deliberate keypress,
			// and it still means what it always meant.
			//
			// Note the asymmetry with the prompt loop below, which goes out of
			// its way to honor a last line that arrives without a trailing
			// newline, because bufio hands that text back alongside the EOF.
			// Here that same text is dropped: `printf 'do X\ny'` denies rather
			// than approving on the "y". The two readers differ because the
			// consequences do. A truncated last line reaching the loop is a
			// garbled question; a truncated last line reaching this switch is
			// consent, and any text the keywords do not match approves through
			// the default arm, so "ye" from a half-written pipe would run the
			// command. Text that arrives with the EOF that ended the stream
			// cannot be told apart from text that was cut off, so it is not
			// treated as an answer.
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					// Gone for good: nothing later in this session can be
					// answered either, so end it rather than prompting on.
					stdinClosed.Store(true)
					deniedNoInput.Store(true)
					fmt.Fprintln(out, theme.Error.Render(theme.Cross+" "+theme.CLIDeniedNoInput))
				} else {
					fmt.Fprintln(out, theme.Error.Render(theme.Cross+" "+theme.CLIDeniedNoAnswer))
				}
				return chat.ToolCallResponse{Approved: false}
			}

			var response chat.ToolCallResponse
			switch strings.ToLower(text) {
			case "y", "yes", "1":
				response = chat.ToolCallResponse{Approved: true}
				spin.start(theme.Status(theme.VerbWorking, 0))
			case "a", "always", "2":
				response = chat.ToolCallResponse{Approved: true, AlwaysAllow: true, AlwaysPrefix: prefix}
				if prefix != "" {
					fmt.Fprintln(out, theme.Subtle.Render("allowing "+prefix+" … commands for this session"))
				} else {
					fmt.Fprintln(out, theme.Subtle.Render("added '"+req.Name+"' to the session allow list"))
				}
				spin.start(theme.Status(theme.VerbWorking, 0))
			case "all", "3":
				response = chat.ToolCallResponse{Approved: true, AllowAllTurn: true}
				fmt.Fprintln(out, theme.Subtle.Render("approving all tool calls for this turn"))
				spin.start(theme.Status(theme.VerbWorking, 0))
			case "n", "no":
				response = chat.ToolCallResponse{Approved: false}
				fmt.Fprintln(out, theme.Error.Render(theme.Cross+" denied"))
			default:
				response = chat.ToolCallResponse{Approved: true, Adjustment: text}
				spin.start(theme.Status(theme.VerbWorking, 0))
			}
			return response
		},
		OnResponse: func(response string) {
			spin.stop()
			fmt.Fprintln(out)
			fmt.Fprintln(out, theme.LabelNib.Render(theme.BrandName)+" "+theme.SepStyle.Render(theme.Sep))
			fmt.Fprintln(out, response)
			fmt.Fprintln(out)
		},
		// The model's step commentary ("I'll search for X now…") — printed dim,
		// before the tool it announced runs, so the transcript reads in order.
		OnStepContent: func(content string) {
			spin.stop()
			for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
				fmt.Fprintln(out, "  "+theme.Subtle.Render(line))
			}
			spin.start(theme.Status(theme.VerbWorking, 0))
		},
		// Both notices fire from the agent's goroutine while the spinner may be
		// mid-frame, so they go through writeNotice rather than Fprintln: an
		// 80ms redraw and a bare print share the line otherwise, and the user
		// reads "⠋ working…pruned 2 tool results".
		OnCompactDone: func(before, after int) {
			writeNotice(out, spin, theme.Subtle.Render(compactNotice(before, after)))
		},
		OnPruneDone: func(results, freed int) {
			writeNotice(out, spin, theme.Subtle.Render(pruneNotice(results, freed)))
		},
		OnError: func(err error) {
			spin.stop()
			fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+err.Error()))
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
			fmt.Fprintln(out, theme.Subtle.Render(theme.Sep+" "+label))
			for _, line := range strings.Split(preview, "\n") {
				fmt.Fprintln(out, theme.Help.Render("  "+line))
			}
			spin.start(theme.Status(theme.VerbThinking, 0))
		},
		OnAgentEvent: func(ev chat.AgentEvent) {
			spin.stop()
			fmt.Fprintln(out, formatAgentEventLine(ev))
			spin.start(theme.Status(theme.VerbThinking, 0))
		},
	}

	session, err := chat.NewSession(ctx, cfg, callbacks, transports...)
	if err != nil {
		return err
	}
	defer session.Close()
	// Registered after the Close defer so it runs BEFORE it (defers are LIFO)
	// and the session is still readable. A defer rather than a line at each
	// return: RunCLI has several exit paths and the summary belongs on all of
	// them.
	//
	// errOut, not out: stdout carries the transcript a caller may be piping, and
	// the summary would land in the middle of it.
	defer func() {
		if s := chat.FormatSessionSummary(session.Usage()); s != "" {
			fmt.Fprintln(errOut, theme.Help.Render(s))
		}
	}()
	if shellJobs != nil {
		// Keep a run parked while a background shell job is still running and
		// inject its completion notice, so bash_background work isn't orphaned.
		session.SetShellJobs(shellJobs)
	}

	fmt.Fprintln(out, theme.Brand.Render(theme.BrandName))
	fmt.Fprintln(out, theme.Rule.Render(strings.Repeat("─", 50)))
	fmt.Fprintln(out, theme.Help.Render(theme.CLIWelcome))
	fmt.Fprintln(out, theme.Help.Render(theme.CLIExit))
	if cfg.ApprovalMode == "auto" {
		fmt.Fprintln(out, theme.Yolo.Render(theme.YoloNotice))
	}
	fmt.Fprintln(out)

	// Display help immediately
	help(out)

	// Files staged via /attach, sent with the next message and cleared on
	// successful send only.
	var pending []attachstage.StagedFile

	// stdinClosed, set here or by the approval callback, ends the loop. Running
	// out of input is the end of the input, not a failure: `echo "question" |
	// nib --cli` has to answer and exit 0, and an interactive Ctrl-D is the
	// same EOF and ends the session the same way. Only EOF; any other read
	// error is still reported, so a stdin that is genuinely broken does not
	// look like a session the user finished. A session that had to refuse a
	// tool call along the way ends with ErrApprovalNoInput instead, because
	// that is not a success either.
	//
	// It is a flag checked at the top of the loop rather than an immediate
	// return because a last line arriving without a trailing newline
	// (`printf 'question' | nib --cli`) comes back from bufio alongside the
	// EOF. Letting the body run for it and stopping on the next trip is what
	// keeps that line from being dropped.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Inside the default arm, not above the select: a ready ctx.Done()
			// always wins over default, so cancellation is answered first. An
			// EOF followed by a Ctrl+C is still a Ctrl+C, and reporting it as
			// the clean end of input would invert the very distinction the EOF
			// handling exists to keep.
			if stdinClosed.Load() {
				if deniedNoInput.Load() {
					return ErrApprovalNoInput
				}
				return nil
			}

			fmt.Fprint(out, theme.Prompt.Render(theme.PromptGlyph)+" ")

			text, err := readStringCancellable(ctx, reader)
			switch {
			case errors.Is(err, io.EOF):
				stdinClosed.Store(true)
			case err != nil:
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
				help(out)
				continue
			}

			action := resolveCLIInput(text, cfg)
			switch action.Kind {
			case slash.KindError:
				fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+action.Err))
				continue
			case slash.KindLoadSkill:
				notice, err := session.LoadSkill(action.Skill)
				if err != nil {
					fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+err.Error()))
				} else {
					fmt.Fprintln(out, theme.Subtle.Render(notice))
				}
				continue
			case slash.KindCompact:
				spin.start(theme.Status(theme.VerbThinking, 0))
				before, after, err := session.CompactHistory()
				spin.stop()
				if err != nil {
					fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+err.Error()))
				} else if before == after {
					fmt.Fprintln(out, theme.Subtle.Render("Nothing to compact yet."))
				} else {
					fmt.Fprintln(out, theme.Subtle.Render(compactNotice(before, after)))
				}
				continue
			case slash.KindModelList:
				// Bounded like the switch below: the user is waiting at the
				// prompt, so an endpoint that accepts the connection and never
				// answers must not wedge the loop.
				listCtx, cancel := context.WithTimeout(ctx, chat.ModelListTimeout)
				models, err := session.ListModels(listCtx)
				cancel()
				if err != nil {
					fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+err.Error()))
				} else {
					// Raw, like the assistant's own reply: these are names the
					// user reads and copies, and the marker column is the
					// emphasis the listing needs.
					fmt.Fprint(out, chat.FormatModelList(models, session.Model()))
				}
				continue
			case slash.KindModelSet:
				notice, err := session.SwitchModel(ctx, action.Model)
				if err != nil {
					fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+err.Error()))
				} else {
					fmt.Fprintln(out, theme.Subtle.Render(notice))
				}
				continue
			case slash.KindAttach:
				switch action.AttachOp {
				case slash.AttachStage:
					pending = append(pending, attachstage.StagedFile{Path: action.AttachPath, Transcribe: action.Transcribe})
					mode := "default"
					if action.Transcribe {
						mode = "transcribe"
					}
					fmt.Fprintln(out, theme.Subtle.Render("attached: "+filepath.Base(action.AttachPath)+" ("+mode+") — sends with your next message"))
				case slash.AttachList:
					if len(pending) == 0 {
						fmt.Fprintln(out, theme.Subtle.Render("nothing staged"))
					} else {
						for _, s := range pending {
							fmt.Fprintln(out, theme.Subtle.Render("  "+filepath.Base(s.Path)))
						}
					}
				case slash.AttachClear:
					n := len(pending)
					pending = nil
					fmt.Fprintln(out, theme.Subtle.Render(fmt.Sprintf("cleared %d staged attachment(s)", n)))
				}
				continue
			default: // slash.KindSend
				fmt.Fprintln(out)
				spin.start(theme.Status(theme.VerbThinking, 0))
				files, overrides := attachstage.BuildSend(pending, action)
				if len(files) == 0 {
					_, err = session.SendMessage(action.Text)
					spin.stop()
				} else {
					var blocked []attachments.Blocked
					_, blocked, err = session.SendWithAttachments(ctx, action.Text, files, overrides)
					spin.stop()
					for _, b := range blocked {
						fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+filepath.Base(b.Path)+" — "+b.Reason))
					}
					if err == nil {
						pending = nil // clear on success only
					}
				}
				if err != nil {
					fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+err.Error()))
				}
				fmt.Fprintln(out)
			}
		}
	}
}

// pruneNotice formats the one-line summary shown when tool output is pruned.
//
// It does not say "stale": the high-water sweep picks the oldest LARGE results
// purely on size, and nothing about those is stale, so the word would tell the
// user their still-valid read output had gone bad. The count and the token
// figure carry everything the notice has to say.
//
// The saving is rendered with HumanTokensOrZero rather than HumanTokens: a
// stale read is stubbed however small it was, so a pass can free nothing
// measurable, and HumanTokens renders 0 as "" — leaving the sentence a hole
// where its number belongs.
func pruneNotice(results, freed int) string {
	noun := "results"
	if results == 1 {
		noun = "result"
	}
	return fmt.Sprintf("pruned %d tool %s — freed %s tokens", results, noun, chat.HumanTokensOrZero(freed))
}

// compactNotice formats the one-line summary shown after a conversation is
// compacted. The figures are byte/4 estimates rather than the backend's
// reported usage, so they are marked approximate — an unmarked number reads as
// measured, and a user cannot tell the difference.
//
// Marked rather than replaced with real usage: the "after" side has never been
// sent to a backend when this prints, so no reported figure for it exists to
// use. Session.Usage() answers a different question (what the session spent),
// not what the conversation now weighs.
func compactNotice(before, after int) string {
	return fmt.Sprintf("Compacted conversation — ~%s → ~%s tokens (estimated)",
		chat.HumanTokens(before), chat.HumanTokens(after))
}

func help(out io.Writer) {
	fmt.Fprintln(out, theme.Help.Render("commands:  exit  ·  clear  ·  help"))
}
