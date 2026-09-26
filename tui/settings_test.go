package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// End to end through the key handler: the typed line reaches /settings rather
// than being swallowed by the popup, and Enter on "/settings " lists instead
// of picking the first key.
func TestSettingsEnterSubmitsThroughThePopup(t *testing.T) {
	m, path := newSettingsTestModel(t)
	m.textarea.SetValue("/settings ui.hide_hud on")
	m.completion.sync(m.textarea.Value())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.cfg.UI.HideHUD {
		t.Fatalf("Enter did not apply the setting; last message %+v", lastMessage(t, m))
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "hide_hud: true") {
		t.Fatalf("file:\n%s", data)
	}

	m.textarea.SetValue("/settings ")
	m.completion.sync(m.textarea.Value())
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if msg := lastMessage(t, m); !strings.Contains(msg.Content, "settings · ") {
		t.Fatalf("Enter on /settings did not list: %+v", msg)
	}
}

// newSettingsTestModel returns a model whose config root is a temp dir, so
// /settings writes land there and never in the developer's real config.
func newSettingsTestModel(t *testing.T) (Model, string) {
	t.Helper()
	m := newQueueTestModel()
	m.cfg.BaseDir = t.TempDir()
	m.width = 200
	return m, filepath.Join(m.cfg.BaseDir, "config.yaml")
}

func withHUD(m Model) Model {
	m.hudClock = "12:34:56"
	m.hudCPU, m.hudCPUOK = 42, true
	m.hudMemUsed, m.hudMemTotal = 4<<30, 16<<30
	return m
}

func TestFooterBadgesHonorHideHUD(t *testing.T) {
	m := withHUD(newQueueTestModel())
	m.width = 200
	shown := m.footerBadges(10)
	for _, want := range []string{"cpu", "mem", "12:34:56"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("badges %q lack %q with the HUD shown", shown, want)
		}
	}
	m.cfg.UI.HideHUD = true
	hidden := m.footerBadges(10)
	for _, gone := range []string{"cpu", "mem", "12:34:56"} {
		if strings.Contains(hidden, gone) {
			t.Fatalf("badges %q still show %q with ui.hide_hud on", hidden, gone)
		}
	}
}

func TestSettingsHideHUDHidesBadgesAndPersists(t *testing.T) {
	m, path := newSettingsTestModel(t)
	m = withHUD(m)

	if cmd := m.dispatchResolved("/settings ui.hide_hud on"); cmd != nil {
		t.Fatal("/settings must not start a turn")
	}
	if msg := lastMessage(t, m); msg.Role == "error" {
		t.Fatalf("set failed: %s", msg.Content)
	}
	if b := m.footerBadges(10); strings.Contains(b, "cpu") || strings.Contains(b, "mem") {
		t.Fatalf("badges %q still show cpu/mem after /settings ui.hide_hud on", b)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "hide_hud: true") {
		t.Fatalf("config file = %q, %v; want hide_hud: true", data, err)
	}
	if msg := lastMessage(t, m); strings.Contains(msg.Content, "next start") {
		t.Fatalf("a UI key applies live, got %q", msg.Content)
	}

	// default removes the key and brings the badges back.
	m.dispatchResolved("/settings ui.hide_hud default")
	if b := m.footerBadges(10); !strings.Contains(b, "cpu") {
		t.Fatalf("badges %q after reset, want cpu back", b)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "hide_hud") {
		t.Fatalf("reset left the key in the file:\n%s", data)
	}
}

func TestSettingsApprovalModeAppliesLive(t *testing.T) {
	m, _ := newSettingsTestModel(t)
	m.session = &chat.Session{}
	m.dispatchResolved("/settings approval_mode auto")
	if !m.session.AutoApprove() || m.cfg.ApprovalMode != "auto" {
		t.Fatalf("approval_mode auto: autoApprove %v cfg %q", m.session.AutoApprove(), m.cfg.ApprovalMode)
	}
	m.dispatchResolved("/settings approval_mode prompt")
	if m.session.AutoApprove() {
		t.Fatal("approval_mode prompt must turn auto-approve off")
	}
}

func TestSettingsCompactionUpdatesRunningConfig(t *testing.T) {
	m, _ := newSettingsTestModel(t)
	m.cfg.Compaction = types.CompactionConfig{Threshold: 0.8, KeepRecent: 8, ReserveTokens: 4096}
	m.dispatchResolved("/settings compaction.threshold 0.5")
	if m.cfg.Compaction.Threshold != 0.5 {
		t.Fatalf("threshold = %v, want 0.5", m.cfg.Compaction.Threshold)
	}
	// Siblings the file never set keep their defaults, not zero.
	if m.cfg.Compaction.KeepRecent != 8 {
		t.Fatalf("keep_recent = %d, want the default 8 kept", m.cfg.Compaction.KeepRecent)
	}
}

func TestSettingsNonLiveKeySaysNextStart(t *testing.T) {
	m, path := newSettingsTestModel(t)
	m.dispatchResolved("/settings log_level debug")
	msg := lastMessage(t, m)
	if msg.Role == "error" || !strings.Contains(msg.Content, "next start") {
		t.Fatalf("notice = %q, want a next-start note", msg.Content)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "log_level: debug") {
		t.Fatalf("file:\n%s", data)
	}
}

func TestSettingsRejectsBadInput(t *testing.T) {
	m, path := newSettingsTestModel(t)
	m.dispatchResolved("/settings ui.hide_hd on")
	if msg := lastMessage(t, m); msg.Role != "error" || !strings.Contains(msg.Content, "ui.hide_hud") {
		t.Fatalf("unknown key: %+v, want an error suggesting ui.hide_hud", msg)
	}
	m.dispatchResolved("/settings ui.hide_hud maybe")
	if msg := lastMessage(t, m); msg.Role != "error" {
		t.Fatalf("bad bool: %+v, want an error", msg)
	}
	m.dispatchResolved("/settings api_key sk-123")
	if msg := lastMessage(t, m); msg.Role != "error" || strings.Contains(msg.Content, "sk-123") {
		t.Fatalf("secret: %+v, want a refusal that does not echo the value", msg)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("rejected input wrote the config file (%v)", err)
	}
}

func TestSettingsListAndShow(t *testing.T) {
	m, path := newSettingsTestModel(t)
	m.dispatchResolved("/settings ui.hide_hud on")
	m.dispatchResolved("/settings")
	list := lastMessage(t, m).Content
	for _, want := range []string{"ui.hide_hud", "compaction.threshold", "file", "default", shortenPath(path)} {
		if !strings.Contains(list, want) {
			t.Fatalf("listing lacks %q:\n%s", want, list)
		}
	}
	// A precise check on the key column, not a substring scan of the whole
	// block: config.reflectSettings legitimately lists
	// prompt_injection_protection.classifier.api_key_env (the NAME of an
	// env var, never a secret value — config.isSecretKey only strips the
	// leaf "api_key" itself), and a bare substring scan for "api_key" would
	// trip on that row too. Match the key column exactly instead.
	for _, line := range strings.Split(list, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && (fields[0] == "api_key" || strings.HasSuffix(fields[0], ".api_key")) {
			t.Fatalf("listing shows the secret key api_key:\n%s", list)
		}
	}

	m.dispatchResolved("/settings compaction.threshold")
	show := lastMessage(t, m).Content
	if !strings.Contains(show, "float") || !strings.Contains(show, "compaction") {
		t.Fatalf("show = %q, want the type and doc", show)
	}
}

func TestSettingsCompletionKeys(t *testing.T) {
	var c compState
	c.setRegistries(nil, nil, nil)
	var cfg types.Config
	cfg.Compaction.Threshold = 0.8
	c.setSettingsConfig(cfg)

	c.sync("/sett")
	if it, ok := c.current(); !ok || it.Insert != "/settings " {
		t.Fatalf("verb completion = %+v, %v", it, ok)
	}

	// Several blocks have a threshold now; a dotted prefix picks one.
	c.sync("/settings compaction.thresh")
	if !c.active || len(c.matches) != 1 {
		t.Fatalf("matches = %+v", c.matches)
	}
	it := c.matches[0]
	if it.Name != "compaction.threshold" || it.Insert != "/settings compaction.threshold " || !strings.Contains(it.Desc, "0.8") {
		t.Fatalf("key item = %+v", it)
	}

	// Other verbs keep today's behaviour: the popup closes once args begin.
	c.sync("/yolo o")
	if c.active {
		t.Fatal("argument completion leaked to another verb")
	}
}

func TestSettingsCompletionValues(t *testing.T) {
	var c compState
	c.setRegistries(nil, nil, nil)

	c.sync("/settings ui.hide_hud ")
	names := func() []string {
		var out []string
		for _, it := range c.matches {
			out = append(out, it.Name)
		}
		return out
	}
	if got := strings.Join(names(), ","); got != "on,off,default" {
		t.Fatalf("bool values = %s", got)
	}
	if it, _ := c.current(); it.Insert != "/settings ui.hide_hud on " {
		t.Fatalf("insert = %q", it.Insert)
	}

	c.sync("/settings approval_mode str")
	if got := strings.Join(names(), ","); got != "strict" {
		t.Fatalf("enum values = %s", got)
	}

	// A free-form string has nothing to offer, and a finished value closes.
	if c.sync("/settings model "); c.active {
		t.Fatal("free-form key opened a value popup")
	}
	if c.sync("/settings ui.hide_hud on "); c.active {
		t.Fatal("popup still open after the value was complete")
	}
	if c.sync("/settings nope.key "); c.active {
		t.Fatal("unknown key opened a value popup")
	}
}

// Enter on a fully typed value submits instead of re-accepting it.
func TestSettingsCompletionExactValueSubmits(t *testing.T) {
	var c compState
	c.setRegistries(nil, nil, nil)
	c.sync("/settings ui.hide_hud on")
	if !c.exact("/settings ui.hide_hud on") {
		t.Fatal("a fully typed value should count as exact")
	}
	// Nothing typed for the argument yet: Enter runs the line as it stands
	// (list, or show the key) rather than taking the first row.
	for _, line := range []string{"/settings ", "/settings ui.hide_hud "} {
		c.sync(line)
		if !c.active || !c.exact(line) {
			t.Fatalf("%q: active %v exact %v, want an open popup that Enter submits past", line, c.active, c.exact(line))
		}
	}
	c.sync("/settings ui.hi")
	if c.exact("/settings ui.hi") {
		t.Fatal("a partial key must not count as exact")
	}
}

// TestSettingModelOverriddenByNamedEndpointSaysNotInUse covers the case the
// brief's own rule (gate on EndpointID() != chat.ConfigProviderID) got
// right: the session is on a named config.yaml endpoint, so config.yaml's
// own model is not what runs.
func TestSettingModelOverriddenByNamedEndpointSaysNotInUse(t *testing.T) {
	m := newEndpointTestModel(t)
	if cmd := m.dispatchResolved("/endpoint @home"); cmd != nil {
		t.Fatal("/endpoint <id> must not start a turn")
	}
	if got := m.session.EndpointID(); got != "@home" {
		t.Fatalf("EndpointID = %q, want @home", got)
	}

	m.dispatchResolved("/settings model something-else")
	msg := lastMessage(t, m)
	if msg.Role == "error" {
		t.Fatalf("set failed: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "not in use") || !strings.Contains(msg.Content, "/endpoint config") {
		t.Fatalf("no override notice naming the way back:\n%s", msg.Content)
	}
	if strings.Contains(msg.Content, "applies on next start") {
		t.Fatalf("still claims it applies next start:\n%s", msg.Content)
	}
	if _, pending := m.pendingSettings["model"]; pending {
		t.Fatalf("an overridden key must not be recorded as pending: %+v", m.pendingSettings)
	}

	m.dispatchResolved("/settings")
	if list := lastMessage(t, m).Content; !strings.Contains(list, "overridden") {
		t.Fatalf("listing does not mark the key overridden:\n%s", list)
	}
}

// TestSettingModelOverriddenOnTheDefaultEndpointSaysNotInUse is the case the
// brief's rule (gate on endpoint identity) would have gotten WRONG: SetModel
// persists a model pick on every endpoint uniformly, including config.yaml's
// own default one, so a session can be sitting ON the default endpoint
// (EndpointID() == chat.ConfigProviderID) and still be running a saved
// model that shadows config.yaml's. /settings must still say the write to
// "model" is inert, or it reintroduces the silent-no-op bug this command
// exists to kill.
func TestSettingModelOverriddenOnTheDefaultEndpointSaysNotInUse(t *testing.T) {
	m := newModelSwitchTestModel(t, "default-model", "something-else")

	// Pick a different model without switching endpoints at all: the saved
	// pick now overrides config.yaml's "default-model" while the session
	// stays on config.yaml's own endpoint.
	if cmd := m.dispatchResolved("/model something-else"); cmd != nil {
		t.Fatal("/model <name> must not start a turn")
	}
	if msg := lastMessage(t, m); msg.Role == "error" {
		t.Fatalf("/model something-else failed: %s", msg.Content)
	}
	if got := m.session.EndpointID(); got != chat.ConfigProviderID {
		t.Fatalf("EndpointID = %q, want the default endpoint (%q) — this is the case that matters", got, chat.ConfigProviderID)
	}
	if got := m.session.Model(); got != "something-else" {
		t.Fatalf("Model() = %q, want the saved pick something-else", got)
	}
	if got := m.session.ConfigModel(); got != "default-model" {
		t.Fatalf("ConfigModel() = %q, want config.yaml's own default-model", got)
	}

	m.dispatchResolved("/settings model yet-another")
	msg := lastMessage(t, m)
	if msg.Role == "error" {
		t.Fatalf("set failed: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "not in use") || !strings.Contains(msg.Content, "/model reset") {
		t.Fatalf("no override notice naming the way back:\n%s", msg.Content)
	}
	if strings.Contains(msg.Content, "applies on next start") {
		t.Fatalf("still claims it applies next start:\n%s", msg.Content)
	}
	if _, pending := m.pendingSettings["model"]; pending {
		t.Fatalf("an overridden key must not be recorded as pending: %+v", m.pendingSettings)
	}

	m.dispatchResolved("/settings")
	if list := lastMessage(t, m).Content; !strings.Contains(list, "overridden") {
		t.Fatalf("listing does not mark the key overridden:\n%s", list)
	}
}

// TestSettingModelOverrideOnAModelLessDefaultEndpointDoesNotAdviseReset is
// Finding 3: config.yaml's default endpoint here names no model of its own,
// so /model reset (Session.ResetModel) would refuse with "names no model of
// its own: pick one with /model" — the override notice must not point the
// user at that dead end.
func TestSettingModelOverrideOnAModelLessDefaultEndpointDoesNotAdviseReset(t *testing.T) {
	cfg := types.Config{
		BaseURL:    "http://127.0.0.1:1/v1",
		BaseDir:    t.TempDir(),
		Compaction: types.CompactionConfig{MaxContextTokens: 128000},
	}
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.SetModel("saved-pick"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if got := s.EndpointID(); got != chat.ConfigProviderID {
		t.Fatalf("EndpointID = %q, want the default endpoint", got)
	}
	if got := s.ConfigModel(); got != "" {
		t.Fatalf("test setup: ConfigModel() = %q, want empty (a model-less default endpoint)", got)
	}

	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s

	m.dispatchResolved("/settings model yet-another")
	msg := lastMessage(t, m)
	if msg.Role == "error" {
		t.Fatalf("set failed: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "not in use") {
		t.Fatalf("no override notice:\n%s", msg.Content)
	}
	if strings.Contains(msg.Content, "/model reset") {
		t.Fatalf("notice advises /model reset, which would fail on a model-less default endpoint:\n%s", msg.Content)
	}
	if !strings.Contains(msg.Content, "/model") {
		t.Fatalf("notice does not point at any escape hatch:\n%s", msg.Content)
	}
}

// TestSettingModelWithNoOverrideIsUnchanged is the control: with nothing
// overriding config.yaml, /settings model behaves exactly as it always has.
func TestSettingModelWithNoOverrideIsUnchanged(t *testing.T) {
	m, _ := newSettingsTestModel(t)
	m.dispatchResolved("/settings model something-else")
	msg := lastMessage(t, m)
	if msg.Role == "error" {
		t.Fatalf("set failed: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "applies on next start") {
		t.Fatalf("expected the usual next-start notice:\n%s", msg.Content)
	}
	if strings.Contains(msg.Content, "not in use") {
		t.Fatalf("unexpected override notice:\n%s", msg.Content)
	}
}

// TestSettingProviderOnANamedEndpointSaysNotInUse is the false-negative case
// a pure value comparison misses: on a named endpoint, config.yaml's
// top-level addressing keys are never consulted at all (endpoint.Set builds
// them from the entry alone), but when NEITHER side names a provider
// explicitly, both the running provider and config.yaml's declared one
// default to the same "openai" — so a comparison of values alone would find
// no divergence and wrongly claim the write "applies on next start" even
// though it is completely inert.
func TestSettingProviderOnANamedEndpointSaysNotInUse(t *testing.T) {
	m := newEndpointTestModel(t)
	if got := m.cfg.Provider; got != "" {
		t.Fatalf("fixture must leave config.yaml's top-level provider unset, got %q", got)
	}
	if cmd := m.dispatchResolved("/endpoint @home"); cmd != nil {
		t.Fatal("/endpoint <id> must not start a turn")
	}
	if got, want := m.session.Provider(), m.session.ConfigProvider(); got != want {
		t.Fatalf("fixture no longer reproduces the false-negative setup: running provider %q, config provider %q (want them equal, both defaulting to openai)", got, want)
	}

	m.dispatchResolved("/settings provider anthropic")
	msg := lastMessage(t, m)
	if msg.Role == "error" {
		t.Fatalf("set failed: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "not in use") || !strings.Contains(msg.Content, "/endpoint config") {
		t.Fatalf("no override notice naming the way back:\n%s", msg.Content)
	}
	if strings.Contains(msg.Content, "applies on next start") {
		t.Fatalf("still claims it applies next start — this is the bug the reviewer caught:\n%s", msg.Content)
	}
	if _, pending := m.pendingSettings["provider"]; pending {
		t.Fatalf("an overridden key must not be recorded as pending: %+v", m.pendingSettings)
	}
}

// TestSettingModelAndBaseURLOnANamedEndpointSayNotInUse rounds out coverage
// for the other two addressing keys on the same named endpoint: both are
// entirely supplied by @home, so config.yaml's top-level values for them
// cannot take effect regardless of what they compare equal or unequal to.
func TestSettingModelAndBaseURLOnANamedEndpointSayNotInUse(t *testing.T) {
	m := newEndpointTestModel(t)
	if cmd := m.dispatchResolved("/endpoint @home"); cmd != nil {
		t.Fatal("/endpoint <id> must not start a turn")
	}

	m.dispatchResolved("/settings base_url http://elsewhere/v1")
	if msg := lastMessage(t, m); msg.Role == "error" || strings.Contains(msg.Content, "applies on next start") || !strings.Contains(msg.Content, "not in use") {
		t.Fatalf("base_url notice = %+v, want a not-in-use override", msg)
	}

	m.dispatchResolved("/settings model something-else")
	if msg := lastMessage(t, m); msg.Role == "error" || strings.Contains(msg.Content, "applies on next start") || !strings.Contains(msg.Content, "not in use") {
		t.Fatalf("model notice = %+v, want a not-in-use override", msg)
	}
}

// Without a classifier, classify is refused before anything is written, so
// the file and the running config keep the mode that is in effect.
func TestSettingsApprovalModeClassifyRefusedWithoutClassifier(t *testing.T) {
	m, path := newSettingsTestModel(t)
	m.session = &chat.Session{}
	m.dispatchResolved("/settings approval_mode classify")
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "classify") {
		t.Fatalf("classify written without a classifier:\n%s", data)
	}
	if m.cfg.ApprovalMode == "classify" {
		t.Fatal("running config switched to classify")
	}
	if msg := lastMessage(t, m); msg.Role != "error" {
		t.Fatalf("want an error, got %+v", msg)
	}
}
