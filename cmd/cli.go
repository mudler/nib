package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/attachments"
	"github.com/mudler/nib/attachstage"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/internal/textdiff"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
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
	tip      string
	stopChan chan struct{}
	doneChan chan struct{}
	tty      bool
	lastLine string // last message printed in non-TTY mode, for de-duplication
	lastTip  string // last tip printed in non-TTY mode, for de-duplication
}

func newSpinner(out io.Writer) *spinner {
	return &spinner{
		out:      out,
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
		tty:      isTerminal(out),
	}
}

// countdownRe matches the part of a retry status that changes each second.
var countdownRe = regexp.MustCompile(`retrying in [0-9hms.]+`)

// printStatic emits a status line once in non-TTY mode, skipping consecutive
// duplicates so a steady "thinking" state produces a single line, not a flood.
// Caller must hold s.mu.
func (s *spinner) printStatic(message string) {
	if message == "" || message == s.lastLine {
		return
	}
	// A retry wait re-announces itself every second to count down. Without
	// a terminal to redraw in, print it once instead of once per second.
	if countdownRe.ReplaceAllString(message, "") == countdownRe.ReplaceAllString(s.lastLine, "") {
		s.lastLine = message
		return
	}
	s.lastLine = message
	fmt.Fprintln(s.out, theme.Help.Render(message))
}

// printTip emits a dim tip line once in non-TTY mode, skipping consecutive
// duplicates. Caller must hold s.mu.
func (s *spinner) printTip(tip string) {
	if tip == "" || tip == s.lastTip {
		return
	}
	s.lastTip = tip
	fmt.Fprintln(s.out, theme.Hint.Render("  "+tip))
}

func (s *spinner) start(message string) {
	s.startWithTip(message, "")
}

// startWithTip starts the spinner with message and, if tip is non-empty,
// prints it as a dim line beneath the spinner. In non-TTY mode the tip is
// printed once after the status line (de-duplicated like the status).
func (s *spinner) startWithTip(message, tip string) {
	if !s.tty {
		s.mu.Lock()
		s.active = true
		s.message = message
		s.tip = tip
		s.printStatic(message)
		s.printTip(tip)
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
	s.tip = tip
	s.stopChan = make(chan struct{})
	s.doneChan = make(chan struct{})
	s.mu.Unlock()

	go func() {
		frames := theme.SpinnerFrames()
		frame := 0
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		defer close(s.doneChan)

		for {
			select {
			case <-s.stopChan:
				s.mu.Lock()
				tipActive := s.tip != ""
				s.mu.Unlock()
				if tipActive {
					// Clear the tip line then the spinner line
					fmt.Fprint(s.out, "\r\033[K\033[A\033[K")
				} else {
					fmt.Fprint(s.out, "\r\033[K")
				}
				return
			case <-ticker.C:
				s.mu.Lock()
				msg := s.message
				t := s.tip
				s.mu.Unlock()
				if t != "" {
					fmt.Fprintf(s.out, "\r%s %s\n\033[K%s", theme.Help.Render(frames[frame]), theme.Help.Render(msg), theme.Hint.Render("  "+t))
				} else {
					fmt.Fprintf(s.out, "\r%s %s", theme.Help.Render(frames[frame]), theme.Help.Render(msg))
				}
				frame = (frame + 1) % len(frames)
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
		s.lastTip = ""
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
	active, msg, tip := s.active, s.message, s.tip
	s.mu.Unlock()
	if !active {
		return func() {}
	}
	s.stop()
	return func() { s.startWithTip(msg, tip) }
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

// cliThinkingLine picks a funny line for the spinner this turn.
// Returns VerbThinking when ui.no_funny is on.
func cliThinkingLine(cfg types.Config) string {
	if cfg.UI.NoFunny {
		return theme.VerbThinking
	}
	return theme.RandomThinkingLine()
}

// cliReasoningLabel picks the header for a reasoning block. Returns "" (the
// plain "reasoning") when ui.no_funny is on.
func cliReasoningLabel(cfg types.Config) string {
	if cfg.UI.NoFunny {
		return ""
	}
	return theme.RandomReasoningLabel()
}

// cliTip picks a tip for display beneath the spinner this turn.
// Returns "" when ui.no_funny is on.
func cliTip(cfg types.Config) string {
	if cfg.UI.NoFunny {
		return ""
	}
	return theme.RandomTip()
}

// startThinkingSpin picks a fresh funny line and tip for the turn and
// starts the spinner with them. Reuses the same line within a turn
// (e.g. after OnReasoning reprints) by passing the existing line/tip.
func startThinkingSpin(spin *spinner, cfg types.Config) {
	line := cliThinkingLine(cfg)
	tip := cliTip(cfg)
	spin.startWithTip(line, tip)
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
			fmt.Fprintln(out, theme.ReasoningHeader(cliReasoningLabel(cfg)))
			for _, line := range strings.Split(strings.TrimRight(reasoning, "\n"), "\n") {
				fmt.Fprintln(out, "  "+theme.Reasoning.Render(line))
			}
			startThinkingSpin(spin, cfg)
		},
		OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
			spin.stop()
			g := theme.Gutter.Render(theme.ApprovalGutter) + " "
			fmt.Fprintln(out)
			fmt.Fprintln(out, g+theme.ApproveKey.Render(req.Name+" wants to run"))
			summary := chat.FormatToolCall(req.Name, req.Arguments)
			var diff textdiff.Diff
			if req.Change != nil {
				diff = req.Change.Diff()
			}
			if !diff.Empty() {
				// The diff replaces the summary's "old -> new" continuation.
				first, _, _ := strings.Cut(summary, "\n")
				fmt.Fprintln(out, g+theme.Help.Render(first)+"  "+theme.Meta.Render(render.DiffStat(diff)))
				for _, row := range render.DiffRows(diff, cliDiffWidth, render.ApprovalDiffRows) {
					fmt.Fprintln(out, g+row)
				}
			} else {
				for _, line := range strings.Split(summary, "\n") {
					fmt.Fprintln(out, g+theme.Help.Render(line))
				}
			}
			if req.Reasoning != "" {
				fmt.Fprintln(out, g+theme.Reasoning.Render(req.Reasoning))
			}
			if req.Verdict != "" {
				fmt.Fprintln(out, g+theme.Meta.Render(theme.ClassifierVerdict+req.Verdict))
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
				spin.start(theme.VerbWorking)
			case "a", "always", "2":
				response = chat.ToolCallResponse{Approved: true, AlwaysAllow: true, AlwaysPrefix: prefix}
				if prefix != "" {
					fmt.Fprintln(out, theme.Subtle.Render("allowing "+prefix+" … commands for this session"))
				} else {
					fmt.Fprintln(out, theme.Subtle.Render("added '"+req.Name+"' to the session allow list"))
				}
				spin.start(theme.VerbWorking)
			case "all", "3":
				response = chat.ToolCallResponse{Approved: true, AllowAllTurn: true}
				fmt.Fprintln(out, theme.Subtle.Render("approving all tool calls for this turn"))
				spin.start(theme.VerbWorking)
			case "n", "no":
				response = chat.ToolCallResponse{Approved: false}
				fmt.Fprintln(out, theme.Error.Render(theme.Cross+" denied"))
			default:
				response = chat.ToolCallResponse{Approved: true, Adjustment: text}
				spin.start(theme.VerbWorking)
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
			spin.start(theme.VerbWorking)
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
		OnAutoApproved: func(req chat.ToolCallRequest, v chat.Verdict) {
			spin.stop()
			call, _, _ := strings.Cut(chat.FormatToolCall(req.Name, req.Arguments), "\n")
			fmt.Fprintln(out, theme.Subtle.Render(fmt.Sprintf(theme.AutoApprovedNotice, v.Category, v.Confidence, call)))
		},
		OnToolResult: func(res chat.ToolResult) {
			preview := chat.PreviewResult(res.Name, res.Result, 12)
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
			startThinkingSpin(spin, cfg)
		},
		OnAgentEvent: func(ev chat.AgentEvent) {
			spin.stop()
			fmt.Fprintln(out, formatAgentEventLine(ev))
			startThinkingSpin(spin, cfg)
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
	if cfg.ApprovalMode == types.ApprovalAuto {
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
				startThinkingSpin(spin, cfg)
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
			case slash.KindModelPick, slash.KindModelList:
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
					fmt.Fprint(out, chat.FormatProviderModelList(session.ActiveProviderName(), models, session.Model()))
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
			case slash.KindYolo:
				// A session-wide flag, same as the TUI's: works perfectly well
				// outside a picker or popup, so it gets a real case rather than
				// falling into the "not available" default below.
				on := !session.AutoApprove()
				if action.YoloOn != nil {
					on = *action.YoloOn
				}
				session.SetAutoApprove(on)
				notice := theme.YoloOff
				if on {
					notice = theme.YoloOn
				}
				fmt.Fprintln(out, theme.Subtle.Render(notice))
				continue
			case slash.KindApprove:
				if action.Mode != "" {
					if err := session.SetApprovalMode(action.Mode); err != nil {
						fmt.Fprintln(out, theme.Error.Render(theme.Cross+" "+err.Error()))
						continue
					}
				}
				mode := session.ApprovalMode()
				if session.AutoApprove() {
					mode = types.ApprovalAuto
				}
				fmt.Fprintln(out, theme.Subtle.Render(fmt.Sprintf(theme.ApproveModeNotice, mode)))
				continue
			case slash.KindClassifier:
				switch {
				case action.ClassifierOff:
					fellBack, _ := session.SetClassifier(types.Config{})
					notice := theme.ClassifierOffNotice
					if fellBack {
						notice += theme.ClassifierFellBack
					}
					fmt.Fprintln(out, theme.Subtle.Render(notice))
				case action.Endpoint != "":
					c, err := chat.ClassifierChoice(cfg.Classifier, action.Endpoint, action.Model)
					if err == nil {
						next := cfg
						next.Classifier = c
						_, err = session.SetClassifier(next)
					}
					if err != nil {
						fmt.Fprintln(out, theme.Error.Render(theme.Cross+" "+err.Error()))
						continue
					}
					fmt.Fprintln(out, theme.Subtle.Render(fmt.Sprintf(theme.ClassifierSet, session.ClassifierInfo())))
				default:
					if info := session.ClassifierInfo(); info != "" {
						fmt.Fprintln(out, theme.Subtle.Render(fmt.Sprintf(theme.ClassifierCurrent, info)))
					} else {
						fmt.Fprintln(out, theme.Subtle.Render(theme.ClassifierNone))
					}
				}
				continue
			case slash.KindLogin:
				if action.Provider == "" {
					fmt.Fprint(out, session.LoginList())
					continue
				}
				// The REPL can run the full login flow (OAuth callback + browser)
				// synchronously — the user is at a terminal.
				RunLoginCommand(types.DefaultProgramName, cfg.BaseDir, []string{action.Provider})
				continue
			case slash.KindLogout:
				if action.Provider == "" {
					fmt.Fprint(out, session.LoginList())
					continue
				}
				notice, err := session.Logout(action.Provider)
				if err != nil {
					fmt.Fprintln(errOut, theme.Error.Render(theme.Cross+" "+err.Error()))
				} else {
					fmt.Fprintln(out, theme.Subtle.Render(notice))
				}
				continue
			case slash.KindSend:
				fmt.Fprintln(out)
				startThinkingSpin(spin, cfg)
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
			case slash.KindAbout:
				fmt.Fprint(out, AboutText(cfg))
				continue
			default:
				// Any Kind without an explicit case above has no CLI meaning:
				// /resume has no picker surface here, and /loop and /goal (the
				// pre-existing hole this task also closes) have nothing in this
				// REPL to drive them either. Refusing here — rather than
				// falling through to KindSend, the previous behavior — is the
				// actual fix: the next Kind slash.Resolve grows lands here
				// automatically instead of being silently sent to the model as
				// chat text.
				msg := fmt.Sprintf(theme.CLINotAvailable, cliKindName(action.Kind))
				if action.Kind == slash.KindResume {
					msg += " " + theme.CLIResumeHint
				}
				fmt.Fprintln(out, theme.Subtle.Render(msg))
				continue
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
//
// It is marked approximate for the same reason compactNotice below is, and in
// the same words: the figure is chat.tokensOf's byte/4 estimate of the bodies
// it replaced, not a backend-reported saving, and an unmarked number reads as
// measured. The COUNT is exact and carries no marker — only the tokens are a
// guess.
func pruneNotice(results, freed int) string {
	noun := "results"
	if results == 1 {
		noun = "result"
	}
	return fmt.Sprintf("pruned %d tool %s — freed ~%s tokens (estimated)", results, noun, chat.HumanTokensOrZero(freed))
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
//
// Both figures use HumanTokensOrZero, like pruneNotice above: HumanTokens
// renders 0 as "", which would print "~ → ~ tokens". A zero "after" cannot
// occur (compaction always leaves a summary), and a zero "before" needs a
// conversation under four bytes — but nothing in the signature says so, and the
// neighbouring notice should not disagree with this one about how a zero reads.
func compactNotice(before, after int) string {
	return fmt.Sprintf("Compacted conversation — ~%s → ~%s tokens (estimated)",
		chat.HumanTokensOrZero(before), chat.HumanTokensOrZero(after))
}

// cliKindName names a resolved slash.Kind for the CLI's "not available"
// notice (theme.CLINotAvailable). Only kinds that can actually reach that
// default arm need an entry here; anything left out still gets refused, just
// with a generic name instead of a specific one.
func cliKindName(k slash.Kind) string {
	switch k {
	case slash.KindLoopStart, slash.KindLoopStop, slash.KindLoopList:
		return "/loop"
	case slash.KindGoalSet, slash.KindGoalShow, slash.KindGoalClear, slash.KindGoalResume:
		return "/goal"
	case slash.KindResume:
		return "/resume"
	case slash.KindSettings:
		return "/settings"
	default:
		return "that command"
	}
}

func help(out io.Writer) {
	fmt.Fprintln(out, theme.Help.Render(theme.CLIHelp))
}

// cliDiffWidth is how wide the line-mode CLI draws an approval diff. The CLI
// writes to a stream, not a sized screen, so it uses a fixed conventional width.
const cliDiffWidth = 80
