package chat

import "testing"

func TestScheduleWakeupClampsAndCalls(t *testing.T) {
	var got WakeupRequest
	tool := &scheduleWakeupTool{schedule: func(r WakeupRequest) string {
		got = r
		return "ok"
	}}

	// Below the floor → clamped up to 5; prompt + reason carried.
	if out, _, _ := tool.Run(map[string]any{"delay_seconds": float64(2), "prompt": "/foo", "reason": "poll build"}); out != "ok" {
		t.Fatalf("Run returned %q", out)
	}
	if got.DelaySeconds != 5 {
		t.Fatalf("min clamp: got %d, want 5", got.DelaySeconds)
	}
	if got.Prompt != "/foo" || got.Reason != "poll build" {
		t.Fatalf("prompt/reason not passed: %+v", got)
	}

	// `note` is accepted as a back-compat alias for prompt.
	tool.Run(map[string]any{"delay_seconds": float64(10), "note": "check bg-2"})
	if got.Prompt != "check bg-2" {
		t.Fatalf("note alias: got prompt %q", got.Prompt)
	}

	// Above the ceiling → clamped down to a day.
	tool.Run(map[string]any{"delay_seconds": float64(99999999), "prompt": "x"})
	if got.DelaySeconds != 86400 {
		t.Fatalf("max clamp: got %d, want 86400", got.DelaySeconds)
	}

	// No scheduler wired → graceful message, no panic.
	if out, _, _ := (&scheduleWakeupTool{}).Run(map[string]any{"delay_seconds": float64(10)}); out == "" {
		t.Fatal("expected a message when scheduling is unavailable")
	}
}

// The tool definition must build AND its schema must generate without panicking
// (cogito reflects the arg struct lazily in Tool()); a map field would blow up here.
func TestScheduleWakeupDefinitionBuilds(t *testing.T) {
	def := scheduleWakeupToolDefinition(func(WakeupRequest) string { return "" })
	if def == nil {
		t.Fatal("nil definition")
	}
	tool := def.Tool()
	if tool.Function == nil || tool.Function.Name != "schedule_wakeup" {
		t.Fatalf("unexpected tool: %+v", tool)
	}
}
