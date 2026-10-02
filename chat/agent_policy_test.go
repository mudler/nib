package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

var policyAgentNames = []string{"spawn_agent", "check_agent", "get_agent_result", "send_agent_message"}

func TestAgentToolPolicyWarmAndExecution(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow []string
		want  []string
	}{
		{"default", nil, policyAgentNames},
		{"spawn only", []string{"spawn_agent"}, []string{"spawn_agent"}},
		{"check only", []string{"check_agent"}, []string{"check_agent"}},
		{"result only", []string{"get_agent_result"}, []string{"get_agent_result"}},
		{"check and result", []string{"check_agent", "get_agent_result"}, []string{"check_agent", "get_agent_result"}},
		{"send only", []string{"send_agent_message"}, []string{"send_agent_message"}},
		{"no agents", []string{"agent_logs"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newWarmTestSession(t)
			s.toolAllow = map[string]bool{}
			for _, n := range tc.allow {
				s.toolAllow[n] = true
			}
			if err := s.Warm(context.Background()); err != nil {
				t.Fatal(err)
			}
			warm := s.llm.(*captureLLM).lastRequest().Tools
			if _, err := s.SendMessage("hello"); err != nil {
				t.Fatal(err)
			}
			actual := s.llm.(*captureLLM).lastRequest().Tools
			// Compare complete wire schemas rather than schema-builder internals.
			w, err := json.Marshal(warm)
			if err != nil {
				t.Fatal(err)
			}
			a, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(w, a) {
				t.Fatalf("Warm and execution schemas differ\nwarm: %s\nactual: %s", w, a)
			}
			names := toolNames(actual)
			for _, n := range policyAgentNames {
				if slices.Contains(names, n) != slices.Contains(tc.want, n) {
					t.Errorf("%s: tools %v, want agents %v", n, names, tc.want)
				}
			}
			if len(tc.allow) > 0 && s.ToolCount() != len(tc.allow) {
				t.Errorf("ToolCount = %d, want %d", s.ToolCount(), len(tc.allow))
			}
		})
	}
	// All four agent tools count individually.
	s := newWarmTestSession(t)
	s.toolAllow = nil
	all := s.ToolCount()
	s.toolAllow = map[string]bool{}
	for _, n := range policyAgentNames {
		s.toolAllow[n] = true
	}
	if s.ToolCount() != 4 {
		t.Fatalf("four agent tools count = %d (unrestricted %d)", s.ToolCount(), all)
	}
}

type policyCountingTool struct{ calls int }

func (p *policyCountingTool) Run(struct{}) (string, any, error) {
	p.calls++
	return "executed", nil, nil
}

type policyCallLLM struct {
	captureLLM
	name string
	args string
	once bool
}

func (p *policyCallLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	p.record(req)
	msg := openai.ChatCompletionMessage{Role: "assistant", Content: "done"}
	if !p.once {
		p.once = true
		msg.Content = ""
		msg.ToolCalls = []openai.ToolCall{{ID: "policy-call", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: p.name, Arguments: p.args}}}
	}
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: msg, FinishReason: openai.FinishReasonStop}}}}, cogito.LLMUsage{}, nil
}

func TestAgentToolPolicyDeniedInheritedSend(t *testing.T) {
	for _, allow := range []map[string]bool{{"spawn_agent": true}, {"check_agent": true, "get_agent_result": true}, {"agent_logs": true}} {
		s := newWarmTestSession(t)
		s.toolAllow = allow
		probe := &policyCountingTool{}
		opts := append(s.toolOptions(context.Background(), "", ""), cogito.WithTools(cogito.NewToolDefinition(probe, struct{}{}, "send_agent_message", "inherited probe")), cogito.WithMaxRetries(1))
		warm := &captureLLM{}
		f := cogito.NewEmptyFragment().AddMessage("user", "send")
		if err := cogito.Prefill(context.Background(), warm, f, opts...); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(toolNames(warm.lastRequest().Tools), "send_agent_message") {
			t.Error("prefill restored excluded inherited send")
		}
		llm := &policyCallLLM{name: "send_agent_message", args: `{}`}
		_, err := cogito.ExecuteTools(llm, f, opts...)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected excluded tool rejection, got %v", err)
		}
		if llm.calls() == 0 {
			t.Fatal("model was not called")
		}
		if probe.calls != 0 {
			t.Fatal("denied inherited send executed")
		}
	}
}

func TestAgentToolPolicyCheckResultExecuteWithoutSpawn(t *testing.T) {
	for _, name := range []string{"check_agent", "get_agent_result"} {
		s := newWarmTestSession(t)
		s.toolAllow = map[string]bool{name: true}
		s.agentManager.Register(&cogito.AgentState{ID: "finished", Status: cogito.AgentStatusCompleted, Result: "policy-result"})
		llm := &policyCallLLM{name: name, args: `{"agent_id":"finished"}`}
		s.llm = llm
		if _, err := s.SendMessage("inspect"); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range llm.lastRequest().Messages {
			if strings.Contains(m.Content, "policy-result") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s returned no agent result: %+v", name, llm.lastRequest().Messages)
		}
	}
}
