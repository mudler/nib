package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/mudler/nib/theme"
)

func TestStartupFailureReplacesStartingNotice(t *testing.T) {
	m := frameModel()
	m.sessionReady = false
	m.boot = newBootState()
	next, _ := m.Update(sessionReadyMsg{err: errors.New("cannot create provider")})
	m = next.(Model)
	view := m.View()
	if strings.Contains(view, theme.Starting) || !strings.Contains(view, "Startup failed") || !strings.Contains(view, "cannot create provider") {
		t.Fatalf("startup failure must replace waiting state and show cause: %s", view)
	}
	if !m.boot.collapsed || m.sessionReady {
		t.Fatal("failed initialization must stop showing boot animation without enabling chat")
	}
}
