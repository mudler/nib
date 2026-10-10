package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func attachmentStatusModel(t *testing.T) Model {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	s, err := chat.NewSession(context.Background(), types.Config{Model: "test", BaseURL: server.URL, WorkingDir: t.TempDir()}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m := newWakeupTestModel()
	m.ctx = context.Background()
	m.session = s
	m.toolEvents = newToolEventQueue()
	m.reconcileStatus()
	return m
}

func TestStatusAttachmentPreExecutionSettlement(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "missing-text"
		if blocked {
			name = "all-blocked"
		}
		t.Run(name, func(t *testing.T) {
			m := attachmentStatusModel(t)
			before := m.session.ActivitySnapshot().RootExecutionSequence
			text, file := "hello", filepath.Join(t.TempDir(), "missing.txt")
			if blocked {
				text, file = "", filepath.Join(t.TempDir(), "unsupported.png")
			}
			cmd := m.sendWithAttachmentsDeliveryCmd(text, []string{file}, nil, chat.InputAutomatic)
			m.reconcileStatus()
			if !m.activitySummary(time.Now()).Updating {
				t.Fatal("dispatch did not invalidate readiness")
			}
			result := cmd().(responseMsg)
			if blocked {
				if result.err != nil || len(result.blocked) != 1 {
					t.Fatalf("expected blocked no-op: %+v", result)
				}
			} else if result.err == nil {
				t.Fatal("expected read failure")
			}
			if s := m.session.ActivitySnapshot(); s.RootExecutionSequence != before || !s.ReadyAllowed {
				t.Fatalf("expected pre-execution return: %+v", s)
			}
			next, _ := m.Update(result)
			m = next.(Model)
			for i := 0; i < 3; i++ {
				next, _ = m.Update(statusRefreshMsg{m.statusTickSequence})
				m = next.(Model)
			}
			if got := m.activitySummary(time.Now()); got.Primary != "Ready for input" || !got.CountsKnown || m.statusPending {
				t.Fatalf("settled attachment stuck pending: %+v", got)
			}
		})
	}
}

func TestStatusAttachmentSettlementOwnership(t *testing.T) {
	for _, transition := range []string{"new-attachment", "new-text", "new-invalidation", "reset", "replacement", "duplicate"} {
		t.Run(transition, func(t *testing.T) {
			m := attachmentStatusModel(t)
			file := filepath.Join(t.TempDir(), "missing.txt")
			old := m.sendWithAttachmentsDeliveryCmd("old", []string{file}, nil, chat.InputAutomatic)().(responseMsg)
			switch transition {
			case "reset":
				m.resetSchedules()
			case "replacement":
				replacement := attachmentStatusModel(t)
				m.session = replacement.session
			case "duplicate":
				next, _ := m.Update(old)
				m = next.(Model)
			}
			switch transition {
			case "new-text":
				m.sendMessageDelivery("new", chat.InputAutomatic)
			case "new-invalidation":
				m.invalidateStatus()
			default:
				m.sendWithAttachmentsDeliveryCmd("new", []string{file}, nil, chat.InputAutomatic)
			}
			m.loading = true
			next, _ := m.Update(old)
			m = next.(Model)
			m.reconcileStatus()
			if !m.statusPending || !m.activitySummary(time.Now()).Updating {
				t.Fatal("obsolete result settled newer pending input")
			}
			if !m.loading {
				t.Fatal("obsolete result changed current operation UI")
			}
		})
	}
}

func TestStatusAttachmentObsoleteResult(t *testing.T) {
	for _, transition := range []string{"reset", "replacement", "duplicate"} {
		t.Run(transition, func(t *testing.T) {
			m := attachmentStatusModel(t)
			result := m.sendWithAttachmentsDeliveryCmd("old", []string{filepath.Join(t.TempDir(), "missing.txt")}, nil, chat.InputAutomatic)().(responseMsg)
			switch transition {
			case "reset":
				m.resetSchedules()
			case "replacement":
				replacement := attachmentStatusModel(t)
				m.session = replacement.session
			case "duplicate":
				next, _ := m.Update(result)
				m = next.(Model)
			}
			m.loading = true
			next, _ := m.Update(result)
			m = next.(Model)
			if !m.loading {
				t.Fatal("obsolete result accepted without a newer dispatch")
			}
		})
	}
}
