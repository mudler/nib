package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func seedReprompt(s *Session) {
	s.runMu.Lock()
	s.goalReprompts.timestamps = []time.Time{time.Now()}
	s.runMu.Unlock()
}

func repromptCount(s *Session) int {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return len(s.goalReprompts.timestamps)
}

func TestRepromptSendDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		delivery   InputDelivery
		want       int
	}{
		{"human", "hello", InputHuman, 0},
		{"whitespace", " \t", InputHuman, 1},
		{"automatic", "notice", InputAutomatic, 1},
		{"accepted", "queued", InputAccepted, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newWarmTestSession(t)
			seedReprompt(s)
			if _, err := s.SendMessageWithDelivery(tc.text, tc.delivery); err != nil {
				t.Fatal(err)
			}
			if got := repromptCount(s); got != tc.want {
				t.Fatalf("budget = %d, want %d", got, tc.want)
			}
		})
	}
	s := newWarmTestSession(t)
	seedReprompt(s)
	if _, err := s.SendMessage("fresh"); err != nil {
		t.Fatal(err)
	}
	if repromptCount(s) != 0 {
		t.Fatal("public wrapper did not accept human input")
	}
}

func TestRepromptInjectDelivery(t *testing.T) {
	for _, delivery := range []InputDelivery{InputHuman, InputAutomatic, InputAccepted} {
		s := newWarmTestSession(t)
		seedReprompt(s)
		if s.InjectWithDelivery("idle", delivery) {
			t.Fatal("idle injection accepted")
		}
		if repromptCount(s) != 1 || len(s.userInjected) != 0 {
			t.Fatal("failed injection changed state")
		}
		s.runLive = true
		if s.InjectWithDelivery(" \t", delivery) {
			t.Fatal("blank accepted")
		}
		if !s.InjectWithDelivery("follow-up", delivery) {
			t.Fatal("live injection rejected")
		}
		want := 1
		if delivery == InputHuman {
			want = 0
		}
		if repromptCount(s) != want {
			t.Fatal("wrong reset")
		}
		tracked := 1
		if delivery == InputAutomatic {
			tracked = 0
		}
		if len(s.userInjected) != tracked {
			t.Fatal("wrong undelivered ownership")
		}
		for len(s.inject) < cap(s.inject) {
			s.Inject("fill")
		}
		seedReprompt(s)
		if s.InjectWithDelivery("full", delivery) {
			t.Fatal("full injection accepted")
		}
		if repromptCount(s) != 1 || len(s.userInjected) != tracked {
			t.Fatal("failed full injection changed state")
		}
	}
	s := newWarmTestSession(t)
	s.runLive = true
	seedReprompt(s)
	if !s.Inject("automatic") || repromptCount(s) != 1 {
		t.Fatal("Inject must remain automatic")
	}
	if !s.InjectUser("human") || repromptCount(s) != 0 {
		t.Fatal("InjectUser must accept human")
	}
}

func TestRepromptAttachmentsDelivery(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("extracted text"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, text string
		delivery   InputDelivery
		want       int
	}{
		{"human", "summarize", InputHuman, 0},
		{"only attachment", "", InputHuman, 1},
		{"whitespace", " \t", InputHuman, 1},
		{"automatic", "summarize", InputAutomatic, 1},
		{"accepted", "summarize", InputAccepted, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newWarmTestSession(t)
			seedReprompt(s)
			if _, _, err := s.SendWithAttachmentsDelivery(context.Background(), tc.text, []string{file}, nil, tc.delivery); err != nil {
				t.Fatal(err)
			}
			if got := repromptCount(s); got != tc.want {
				t.Fatalf("budget = %d, want %d", got, tc.want)
			}
		})
	}
	// Acceptance precedes potentially slow conversion; the core send must not
	// erase reminders that accumulated after that acceptance.
	s := newWarmTestSession(t)
	seedReprompt(s)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if repromptCount(s) != 0 {
			t.Error("human text not accepted before attachment processing")
		}
		seedReprompt(s)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	s.baseURL = srv.URL
	if _, _, err := s.SendWithAttachments(context.Background(), "summarize", []string{file}, nil); err != nil {
		t.Fatal(err)
	}
	if repromptCount(s) != 1 {
		t.Fatal("attachment send accepted twice")
	}
}

func TestRepromptUndeliveredAcceptedRedispatch(t *testing.T) {
	s := newWarmTestSession(t)
	s.AcceptHumanMessage("queued")
	seedReprompt(s) // a reminder after host acceptance must survive delivery
	s.callbacks.OnResponse = func(string) {
		if !s.InjectWithDelivery("queued", InputAccepted) {
			t.Error("accepted injection failed")
		}
		if !s.InjectWithDelivery("notice", InputAutomatic) {
			t.Error("automatic injection failed")
		}
	}
	if _, err := s.SendMessageWithDelivery("wake", InputAutomatic); err != nil {
		t.Fatal(err)
	}
	pending := s.TakeUndelivered()
	if len(pending) != 1 || pending[0] != "queued" {
		t.Fatalf("undelivered = %v", pending)
	}
	s.callbacks.OnResponse = nil
	for _, text := range pending {
		if _, err := s.SendMessageWithDelivery(text, InputAccepted); err != nil {
			t.Fatal(err)
		}
	}
	if repromptCount(s) != 1 {
		t.Fatal("accepted redispatch reset again")
	}
	if len(s.TakeUndelivered()) != 0 {
		t.Fatal("undelivered ownership not transferred")
	}
}

// Consume the first injection directly at the channel boundary, then leave an
// identical automatic delivery for the real end-of-run drain. No timing or
// model decision is involved in which delivery was consumed.
func TestRepromptUndeliveredSameTextOwnership(t *testing.T) {
	for _, delivery := range []InputDelivery{InputHuman, InputAccepted} {
		for _, duplicates := range []bool{false, true} {
			t.Run(fmt.Sprintf("delivery=%d/duplicates=%t", delivery, duplicates), func(t *testing.T) {
				s := newWarmTestSession(t)
				s.callbacks.OnResponse = func(string) {
					consumed := make(chan string)
					go func() {
						consumed <- (<-s.inject).Content
					}()
					if !s.InjectWithDelivery("same text", delivery) {
						t.Error("human injection failed")
					}
					consumedText := <-consumed
					if !s.Inject(consumedText) {
						t.Error("automatic injection failed")
					}
					if duplicates {
						for _, text := range []string{"same text", "between", "same text"} {
							if !s.InjectWithDelivery(text, delivery) {
								t.Error("pending human injection failed")
							}
						}
						if !s.Inject("same text") {
							t.Error("trailing automatic injection failed")
						}
					}
				}
				if _, err := s.SendMessageWithDelivery("wake", InputAutomatic); err != nil {
					t.Fatal(err)
				}
				var want []string
				if duplicates {
					want = []string{"same text", "between", "same text"}
				}
				if got := s.TakeUndelivered(); !slices.Equal(got, want) {
					t.Fatalf("undelivered = %q, want %q", got, want)
				}
				if got := s.TakeUndelivered(); len(got) != 0 {
					t.Fatalf("ownership returned twice: %q", got)
				}
			})
		}
	}
}
