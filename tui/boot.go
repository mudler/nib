package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/theme"
)

// bootEntry is one line in the boot log.
type bootEntry struct {
	ts    string // elapsed timestamp, e.g. "00.041"
	ev    string // event name, e.g. "config"
	dt    string // detail, e.g. "~/.config/nib/config.yaml"
	ready bool   // marks the final READY line
	warn  bool   // a degraded/unexpected condition, rendered in the warning style
}

// bootState tracks the boot log animation. It is a live state, not a gate:
// the user can type immediately and messages queue until READY. The log
// stays visible until the first message is sent, then collapses.
type bootState struct {
	entries   []bootEntry // accumulated lines so far
	index     int         // next scripted event to emit
	done      bool        // sessionReadyMsg received
	collapsed bool        // first message sent; log hidden
	start     time.Time
}

func newBootState() *bootState {
	return &bootState{start: time.Now()}
}

// bootScriptEntry is one scripted boot event with a target delay (ms from start).
type bootScriptEntry struct {
	delayMs int
	ev      string
	dt      string
}

func bootScript() []bootScriptEntry {
	return []bootScriptEntry{
		{0, "core/init", "nib :: starting"},
		{60, "config", ""},
		{120, "provider", ""},
		{200, "model", ""},
		{260, "mcp.connect", ""},
		{400, "tools.mount", ""},
		{460, "skills.index", ""},
		{520, "session", ""},
		{580, "memory", "0 notes loaded"},
	}
}

// bootTickMsg advances the boot animation by emitting the next scripted entry.
type bootTickMsg struct{}

// nextBootCmd returns a command that emits the next boot entry after its delay.
func (b *bootState) nextBootCmd() tea.Cmd {
	if b.index >= len(bootScript()) {
		return nil
	}
	entries := bootScript()
	entry := entries[b.index]
	target := time.Duration(entry.delayMs) * time.Millisecond

	elapsed := time.Since(b.start)
	delay := target - elapsed
	if delay < 0 {
		delay = 0
	}

	return tea.Tick(delay, func(time.Time) tea.Msg {
		return bootTickMsg{}
	})
}

// tick appends the next scripted entry, filling in real data where available.
func (b *bootState) tick(m *Model) {
	if b.index >= len(bootScript()) {
		return
	}
	entries := bootScript()
	e := entries[b.index]
	b.index++

	b.entries = append(b.entries, bootEntry{
		ts: fmt.Sprintf("%05.3f", time.Since(b.start).Seconds()),
		ev: e.ev,
		dt: bootDetail(m, e),
	})
}

// bootDetail is the detail text for a scripted event, filled from m's real
// state where the event has one and from the script's static text otherwise.
func bootDetail(m *Model, e bootScriptEntry) string {
	switch e.ev {
	case "config":
		if m.cfg.BaseDir != "" {
			return m.cfg.BaseDir
		}
		return "defaults"
	case "provider":
		return m.bootProvider()
	case "model":
		return m.bootModel()
	case "mcp.connect":
		n := len(m.transports)
		if n == 0 {
			return "no transports"
		}
		return fmt.Sprintf("connecting %d transports", n)
	case "tools.mount":
		return "tools registered"
	case "skills.index":
		return fmt.Sprintf("%d skills indexed", len(m.cfg.Skills))
	case "session":
		sid := m.sessionID
		if len(sid) > 8 {
			sid = sid[:8]
		}
		if len(m.cfg.InitialHistory) > 0 {
			return fmt.Sprintf("resumed :: %s", sid)
		}
		return fmt.Sprintf("new :: %s", sid)
	}
	return e.dt
}

// bootProvider names the provider requests go to. The session is the source
// of truth: a /login pick saved in provider.json replaces config.yaml's
// endpoint when the session is built, so config.yaml is only a stand-in for
// the moment before the session exists (refreshSession corrects it then).
func (m *Model) bootProvider() string {
	if m.session != nil {
		return m.session.ActiveProviderName()
	}
	if m.cfg.Provider != "" {
		return m.cfg.Provider
	}
	return "openai-compat"
}

// bootModel is the model requests use, from the session like bootProvider.
// When the running model diverges from what config.yaml names for its own
// endpoint, both are configured and only one is used, so the line says which
// instead of leaving the user to guess why the header disagrees with
// config.yaml.
//
// The note is gated on actual divergence — Model() != ActiveEndpointConfigModel()
// — not on which endpoint is active. A /login pick or a named endpoint is the
// usual way the two diverge, but /model default saves a model on every
// endpoint, including config.yaml's own default: a saved pick can
// shadow config.yaml's model while the session never left the default
// endpoint at all, and gating on endpoint identity would hide exactly that
// case. See endpointOverrides in tui/settings.go for the same correction
// applied to /settings.
//
// ActiveEndpointConfigModel (rather than ConfigModel) is what makes this
// correct on a named endpoint too: ConfigModel is hard-wired to config.yaml's
// TOP-LEVEL model, which a named endpoint's own model: key never touches.
func (m *Model) bootModel() string {
	if m.session == nil {
		if m.cfg.Model != "" {
			return m.cfg.Model
		}
		return "default"
	}
	model := m.session.Model()
	cfgModel := m.session.ActiveEndpointConfigModel()
	overridden := model != "" && cfgModel != "" && model != cfgModel
	if model == "" {
		model = "default"
	}
	if overridden {
		model += "  " + fmt.Sprintf(theme.BootModelOverride, cfgModel)
	}
	return model
}

// refreshSession rewrites the provider and model lines once the session
// exists. The session is built concurrently with the animation, so those lines
// are often printed from config.yaml first; left alone they would keep naming
// a model a saved /login pick has already replaced.
func (b *bootState) refreshSession(m *Model) {
	if b == nil {
		return
	}
	for i := range b.entries {
		switch b.entries[i].ev {
		case "provider":
			b.entries[i].dt = m.bootProvider()
		case "model":
			b.entries[i].dt = m.bootModel()
		}
	}
}

// appendStartupWarnings appends one boot entry per config.yaml endpoint
// rejected at load and, if a saved pick could not be honored, one more
// naming what happened instead. Both were previously invisible: the errors
// and the note existed on the session (ConfigErrors, StartupNote) but nothing
// consumed them, so a rejected endpoint or a dropped pick left no trace on
// screen. Called once, right after refreshSession corrects the provider and
// model lines, so the session (and therefore these accessors) is known to
// exist.
func (b *bootState) appendStartupWarnings(m *Model) {
	if m.session == nil {
		return
	}
	for _, err := range m.session.ConfigErrors() {
		b.entries = append(b.entries, bootEntry{
			ts:   fmt.Sprintf("%05.3f", time.Since(b.start).Seconds()),
			ev:   "config.reject",
			dt:   err.Error(),
			warn: true,
		})
	}
	if note := m.session.StartupNote(); note != "" {
		b.entries = append(b.entries, bootEntry{
			ts:   fmt.Sprintf("%05.3f", time.Since(b.start).Seconds()),
			ev:   "endpoint.drop",
			dt:   note,
			warn: true,
		})
	}
}

// markReady flushes remaining scripted entries and appends the READY line.
// Flushed entries get the same real details tick would have given them: a
// session that is ready early must not leave the provider/model lines blank.
func (b *bootState) markReady(m *Model) {
	b.refreshSession(m)
	b.appendStartupWarnings(m)
	script := bootScript()
	for b.index < len(script) {
		e := script[b.index]
		b.index++
		b.entries = append(b.entries, bootEntry{
			ts: fmt.Sprintf("%05.3f", time.Since(b.start).Seconds()),
			ev: e.ev,
			dt: bootDetail(m, e),
		})
	}
	b.entries = append(b.entries, bootEntry{
		ts:    fmt.Sprintf("%05.3f", time.Since(b.start).Seconds()),
		ready: true,
	})
	b.done = true
}

// render produces the boot log string for the body area.
func (b *bootState) render(width int) string {
	if b == nil || b.collapsed {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n")
	for _, e := range b.entries {
		if e.ready {
			sb.WriteString(fmt.Sprintf("  %s  %s\n",
				theme.Meta.Render("["+e.ts+"]"),
				theme.Done.Render("✓ READY")))
			continue
		}
		ev := e.ev
		if len(ev) < 13 {
			ev = ev + strings.Repeat(" ", 13-len(ev))
		}
		mark, evStyled := theme.Done.Render("✓"), theme.Running.Render(ev)
		if e.warn {
			mark, evStyled = theme.Error.Render("!"), theme.Error.Render(ev)
		}
		sb.WriteString(fmt.Sprintf("  %s  %s %s%s\n",
			theme.Meta.Render("["+e.ts+"]"),
			mark,
			evStyled,
			theme.Help.Render(e.dt),
		))
	}
	return sb.String()
}
