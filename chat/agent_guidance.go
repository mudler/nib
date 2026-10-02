package chat

import (
	"context"
	"strings"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// These are instructions, not a capability or security boundary.
const childTaskGuidance = `[Child task guidance]
You are a child working on the supplied task, not the overall orchestrator. Follow the brief and applicable repository instructions. You do not inherit the root conversation; do not invent missing requirements.
Do not spawn or manage other agents. Normal native child tools do not provide that orchestration role. Do not run nested nib/other agent CLIs or use another tool as a workaround. Return needed delegation or follow-up work to the parent.
Stay within assigned scope. Report missing context, conflicts, unavailable capabilities, and blockers to the parent rather than expanding the task.
Return a concise outcome with relevant paths/artifacts, checks actually run and their results, uncertainty, blockers, and suggested follow-ups. Do not claim completion without evidence. Read-only/planning roles are instructions, not promises that mutation is technically impossible.`

func (s *Session) agentDelegationGuidance() string {
	if !s.toolEnabled("spawn_agent") {
		return ""
	}
	b := `

[Root delegation guidance]
Keep the overall plan, integration, acceptance checks, and final answer at the root. Delegate bounded work with a clear independent outcome; do small or tightly coupled work directly. Children cannot dispatch implementation or review agents through normal native tools: do not assign orchestration to them. Collect setup/planning results, then dispatch implementation and independent review from the root when required. Parallelize independent tasks, not conflicting edits.
A child does not inherit this conversation. Give a self-contained brief: objective, relevant context, repository and working-directory/worktree paths, allowed scope and constraints, acceptance checks, and expected report. Specify file/worktree ownership for concurrent edits; spawning does not create isolation. Include only needed context.
Choose an advertised persona appropriate to the task. Persona labels and the tools argument are not a sandbox or guaranteed read-only mode.
spawn_agent with background=false waits: use it for the next dependency. background=true returns an ID immediately: use it when useful independent work can continue, and keep the ID. Started is not completed; do not duplicate a task merely because it has not replied.
Use send_agent_message with the existing ID for clarification, correction, or related follow-up. It injects into running work and resumes finished work from stored context. Spawn again only for a separate task or an explicitly justified restart. Report missing context or tool failures rather than assuming delivery or success.
check_agent reports lifecycle status. get_agent_result retrieves the final result and supports wait=true.`
	if s.toolEnabled("agent_logs") {
		b += " agent_logs shows recent activity when needed."
	}
	b += ` Logs and status are not substitutes for the result. Avoid tight polling: continue independent work or wait for the needed result.
Review returned evidence against acceptance criteria before integrating it. Report files/artifacts, checks and outcomes, failures, and remaining work. Distinguish child-reported checks from checks independently verified by the root. A successful spawn or completed status does not prove correctness.
These are model-facing instructions, not behavioral or security guarantees.`
	if len(s.agentDefs) > 0 {
		b += "\nAvailable sub-agent types:"
		for _, d := range s.agentDefs {
			b += "\n- " + d.Name + ": " + d.Description
		}
	}
	return b
}

// Copy the slice before changing a message; nested fields are left untouched.
// Match the complete harness block, not just its heading, for idempotence.
func childMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	out := append([]openai.ChatCompletionMessage(nil), messages...)
	for _, m := range out {
		if m.Role == "system" && strings.Contains(m.Content, childTaskGuidance) {
			return out
		}
	}
	for i := range out {
		if out[i].Role == "system" {
			out[i].Content += "\n\n" + childTaskGuidance
			return out
		}
	}
	return append([]openai.ChatCompletionMessage{{Role: "system", Content: childTaskGuidance}}, out...)
}

type childGuidedLLM struct{ cogito.LLM }
type childGuidedStreamingLLM struct {
	*childGuidedLLM
	stream cogito.StreamingLLM
}

func guideChildLLM(llm cogito.LLM) cogito.LLM {
	base := &childGuidedLLM{LLM: llm}
	if stream, ok := llm.(cogito.StreamingLLM); ok {
		return &childGuidedStreamingLLM{base, stream}
	}
	return base
}
func (l *childGuidedLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	req.Messages = childMessages(req.Messages)
	return l.LLM.CreateChatCompletion(ctx, req)
}
func (l *childGuidedLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	f.Messages = childMessages(f.Messages)
	return l.LLM.Ask(ctx, f)
}
func (l *childGuidedStreamingLLM) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	req.Messages = childMessages(req.Messages)
	return l.stream.CreateChatCompletionStream(ctx, req)
}
