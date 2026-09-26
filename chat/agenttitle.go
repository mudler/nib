package chat

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

const (
	// agentTitlePrompt asks for a sub-agent's title. The task follows it.
	agentTitlePrompt = "Write a title of at most six words for the task below, " +
		"naming what it does, like a commit subject. No quotes, no trailing period. " +
		"Reply with the title only.\n\nTask:\n"
	// agentTitleTaskBytes is how much of the task the request carries: the
	// title comes from what the task is about, which its start says.
	agentTitleTaskBytes = 2000
	// agentTitleMaxTokens caps the reply. A reasoning model may spend it on
	// thinking and answer nothing, and the task's first sentence stays.
	agentTitleMaxTokens = 256
	// agentTitleTimeout bounds one title request.
	agentTitleTimeout = 60 * time.Second
	// agentTitleWords caps a title in words, whatever the model wrote.
	agentTitleWords = 9
)

// titleAgent asks the model, in the background, for a short title for the
// sub-agent id with task, and reports it with OnAgentTitle. Until then, and
// when it fails, the UI shows the task's first sentence.
//
// The request goes the way a compaction summary does (summaryRequest), so it
// streams when the turns do; it reports no progress. Its spend is counted.
// agent_options.no_titles turns it off: on a backend that serves one request
// at a time, it waits behind the agent's own.
func (s *Session) titleAgent(id, task string) {
	task = strings.TrimSpace(task)
	if s.callbacks.OnAgentTitle == nil || s.cogitoOptions.NoTitles || task == "" {
		return
	}
	// Once per agent: a resumed agent reports running again.
	s.agentMu.Lock()
	if s.agentTitled == nil {
		s.agentTitled = map[string]bool{}
	}
	seen := s.agentTitled[id]
	s.agentTitled[id] = true
	s.agentMu.Unlock()
	if seen {
		return
	}
	if len(task) > agentTitleTaskBytes {
		task = strings.ToValidUTF8(task[:agentTitleTaskBytes], "")
	}
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	go func() {
		ctx, cancel := context.WithTimeout(parent, agentTitleTimeout)
		defer cancel()
		content, usage, err := s.summaryRequest(ctx, openai.ChatCompletionRequest{
			Messages:  []openai.ChatCompletionMessage{{Role: cogito.UserMessageRole.String(), Content: agentTitlePrompt + task}},
			MaxTokens: agentTitleMaxTokens,
		}, nil)
		s.addUsage(usage)
		if err != nil {
			xlog.Debug("sub-agent title request failed", "agent", id, "error", err)
			return
		}
		if title := cleanAgentTitle(content); title != "" {
			s.callbacks.OnAgentTitle(id, title)
		}
	}()
}

var thinkBlock = regexp.MustCompile(`(?s)<think>.*?</think>`)

// cleanAgentTitle turns a model's reply into a one-line title: reasoning tags,
// a "Title:" label, markdown and quotes, and a trailing period go, and it is
// cut to agentTitleWords words. It returns "" when nothing is left.
func cleanAgentTitle(reply string) string {
	reply = thinkBlock.ReplaceAllString(reply, "")
	var line string
	for _, l := range strings.Split(reply, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	line = strings.TrimLeft(line, "# ")
	if len(line) >= 6 && strings.EqualFold(line[:6], "title:") {
		line = line[6:]
	}
	line = strings.NewReplacer("**", "", "__", "", "`", "", "\"", "", "“", "", "”", "").Replace(line)
	line = strings.Trim(strings.TrimSpace(line), "'")
	line = strings.TrimRight(line, ". ")
	words := strings.Fields(line)
	if len(words) > agentTitleWords {
		return strings.Join(words[:agentTitleWords], " ") + "…"
	}
	return strings.Join(words, " ")
}
