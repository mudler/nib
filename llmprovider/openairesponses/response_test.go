package openairesponses

import (
	"errors"
	"strings"
	"testing"
)

func TestTranslateRefusal(t *testing.T) {
	reply, _, err := TranslateResponse([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot help with that."}]}]}`), "model")
	if err != nil || len(reply.ChatCompletionResponse.Choices) != 1 || reply.ChatCompletionResponse.Choices[0].Message.Content != "I cannot help with that." {
		t.Fatalf("lost refusal: %+v %v", reply, err)
	}
}

func TestEmptyResponseIncludesStructuralDetails(t *testing.T) {
	_, _, err := TranslateResponse([]byte(`{"id":"resp_empty","status":"completed","output":[{"type":"reasoning","encrypted_content":"private-content"}]}`), "model")
	if !errors.Is(err, ErrNoResponse) || !strings.Contains(err.Error(), "resp_empty") || !strings.Contains(err.Error(), "reasoning") || strings.Contains(err.Error(), "private-content") {
		t.Fatalf("wrong diagnostics: %v", err)
	}
}
