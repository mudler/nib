package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/nib/types"
)

func TestInterruptCancelsTurn(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Interrupt with no active turn is a safe no-op.
	s.Interrupt()

	turnCtx := s.beginTurn()
	if turnCtx.Err() != nil {
		t.Fatal("fresh turn context should not be cancelled")
	}
	s.Interrupt()
	if !errors.Is(turnCtx.Err(), context.Canceled) {
		t.Fatalf("Interrupt should cancel the turn context, got %v", turnCtx.Err())
	}

	s.endTurn()
	next := s.beginTurn()
	s.endTurn()
	_ = next
	s.Interrupt()
	if s.ctx.Err() != nil {
		t.Fatal("session context must remain alive after interrupts")
	}
}
