package chat

import "testing"

func TestScheduleWakeupClampsAndCalls(t *testing.T) {
	var got WakeupRequest
	tool := &scheduleWakeupTool{schedule: func(r WakeupRequest) string {
		got = r
		return "ok"
	}}

	// Below the floor → clamped up to 5.
	if out, _, _ := tool.Run(map[string]any{"delay_seconds": float64(2), "note": "check bg-2"}); out != "ok" {
		t.Fatalf("Run returned %q", out)
	}
	if got.DelaySeconds != 5 {
		t.Fatalf("min clamp: got %d, want 5", got.DelaySeconds)
	}
	if got.Note != "check bg-2" {
		t.Fatalf("note not passed: %q", got.Note)
	}

	// Above the ceiling → clamped down to a day.
	tool.Run(map[string]any{"delay_seconds": float64(99999999)})
	if got.DelaySeconds != 86400 {
		t.Fatalf("max clamp: got %d, want 86400", got.DelaySeconds)
	}

	// Normal value passes through.
	tool.Run(map[string]any{"delay_seconds": float64(600), "note": "x"})
	if got.DelaySeconds != 600 {
		t.Fatalf("passthrough: got %d, want 600", got.DelaySeconds)
	}

	// No scheduler wired → graceful message, no panic.
	if out, _, _ := (&scheduleWakeupTool{}).Run(map[string]any{"delay_seconds": float64(10)}); out == "" {
		t.Fatal("expected a message when scheduling is unavailable")
	}
}
