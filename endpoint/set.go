// Package endpoint owns the set of model endpoints nib can talk to — the
// config.yaml default, the named endpoints config.yaml declares, and the
// built-in provider registry — and the rule that decides which one a new
// session starts on. It holds no session state and makes no request, so the
// precedence rule is testable on its own.
package endpoint

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

// DefaultID is the ID of config.yaml's top-level block.
const DefaultID = "config"

// DefaultName is how the default endpoint is shown.
const DefaultName = "config.yaml"

// NamedPrefix marks a config.yaml named endpoint, so no yaml name can ever
// shadow (or be shadowed by) a registry provider ID.
const NamedPrefix = "@"

// Kind classifies an Entry.
type Kind string

const (
	KindDefault  Kind = "default"
	KindNamed    Kind = "named"
	KindProvider Kind = "provider"
)

// Entry is one row of the endpoint picker.
type Entry struct {
	ID   string // DefaultID, "@name", or a registry provider ID
	Name string // display name
	Kind Kind
	// Model is what this entry would run, empty when it must be picked.
	Model string
	// Status is a short human description: an address, or a login state.
	Status string
	// Ready means requests can authenticate right now.
	Ready bool
	// Stored means a /login credential exists (so /logout can remove it).
	Stored bool
	// NeedsBaseURL means logging in must also collect an endpoint.
	NeedsBaseURL bool
	// EnvVar is the environment variable that can hold the key instead.
	EnvVar string
	// Def is the registry definition; the zero value for yaml entries.
	Def provider.Definition
}

// Set is the resolved collection of endpoints for one config.
type Set struct {
	cfg   types.Config
	named types.Endpoints
	creds *auth.Store
}

// New builds the set. The returned errors are the rejected yaml endpoints,
// for the caller to show; the set itself is always usable.
func New(cfg types.Config, creds *auth.Store) (*Set, []error) {
	named, errs := cfg.Endpoints.Validate()
	return &Set{cfg: cfg, named: named, creds: creds}, errs
}

// List returns the default endpoint, the named endpoints in file order, then
// the provider registry in its display order.
func (s *Set) List() []Entry {
	out := []Entry{s.defaultEntry()}
	for _, ep := range s.named {
		out = append(out, s.namedEntry(ep))
	}
	stored := map[string]auth.Credential{}
	if creds, err := s.creds.All(); err == nil {
		for _, c := range creds {
			stored[c.ProviderID] = c
		}
	}
	for _, d := range provider.All() {
		out = append(out, providerEntry(d, stored))
	}
	return out
}

// Lookup finds one entry by ID.
func (s *Set) Lookup(id string) (Entry, bool) {
	for _, e := range s.List() {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

func (s *Set) defaultEntry() Entry {
	main := s.cfg.ResolvedMainModel()
	return Entry{
		ID: DefaultID, Name: DefaultName, Kind: KindDefault,
		Model: main.Model, Ready: true, Status: describe(main),
	}
}

func (s *Set) namedEntry(ep types.Endpoint) Entry {
	cfg := s.endpointConfig(ep)
	return Entry{
		ID: NamedPrefix + ep.Name, Name: NamedPrefix + ep.Name, Kind: KindNamed,
		Model: cfg.Model, Ready: true, Status: describe(cfg),
	}
}

func providerEntry(d provider.Definition, stored map[string]auth.Credential) Entry {
	e := Entry{
		ID: d.ID, Name: d.Name, Kind: KindProvider,
		NeedsBaseURL: d.NeedsBaseURL(), EnvVar: d.EnvVar, Def: d,
	}
	switch c, has := stored[d.ID]; {
	case has:
		e.Ready, e.Stored = true, true
		e.Status = "logged in · " + c.StatusLine()
	case d.EnvVar != "" && os.Getenv(d.EnvVar) != "":
		e.Ready, e.Status = true, "key from $"+d.EnvVar
	case d.LoginKind == provider.LoginNone:
		// Local servers need nothing; cloud SDK providers (Bedrock, Vertex)
		// read their own environment at request time.
		e.Ready, e.Status = true, "no login needed"
	default:
		e.Status = "not logged in"
	}
	return e
}

// describe is the status column for an endpoint nib addresses itself: the
// model and host it would use, or a promise to list models when opened.
func describe(c types.ModelProviderConfig) string {
	where := host(c.BaseURL)
	if where == "" {
		where = c.Provider
	}
	switch {
	case c.Model == "" && where == "":
		return "models listed on open"
	case c.Model == "":
		return where + " · models listed on open"
	case where == "":
		return c.Model
	default:
		return c.Model + " @ " + where
	}
}

// host trims a base URL to host[:port] for the status column, which is the
// tightest line in the picker. A URL it cannot parse is returned whole.
func host(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	return u.Host
}

// Config resolves an entry to the connection config a client is built from.
func (s *Set) Config(id string) (types.ModelProviderConfig, error) {
	if id == DefaultID || id == "" {
		return s.cfg.ResolvedMainModel(), nil
	}
	if name, ok := strings.CutPrefix(id, NamedPrefix); ok {
		for _, ep := range s.named {
			if ep.Name == name {
				return s.endpointConfig(ep), nil
			}
		}
		return types.ModelProviderConfig{}, fmt.Errorf("unknown endpoint %q", id)
	}
	def, ok := provider.Get(id)
	if !ok {
		return types.ModelProviderConfig{}, fmt.Errorf("unknown provider %q", id)
	}
	main := s.cfg.ResolvedMainModel()
	p := types.ModelProviderConfig{
		Provider:        def.ID,
		Metadata:        main.Metadata,
		ReasoningEffort: main.ReasoningEffort,
	}
	// config.yaml may already point at this provider with its own key: keep
	// it, unless its base_url sends the provider somewhere else entirely.
	if cfg := s.cfg; cfg.Provider == def.ID &&
		(cfg.BaseURL == "" || strings.TrimRight(cfg.BaseURL, "/") == strings.TrimRight(def.BaseURL, "/")) {
		p.APIKey, p.BaseURL = cfg.APIKey, cfg.BaseURL
	}
	if c, ok, err := s.creds.Get(def.ID); err == nil && ok && c.BaseURL != "" {
		p.BaseURL = c.BaseURL
	}
	return p, nil
}

// endpointConfig resolves one named endpoint. Addressing comes from the
// entry alone — an inherited base_url would silently point it at the wrong
// host — while behavior falls back to the top-level block.
func (s *Set) endpointConfig(ep types.Endpoint) types.ModelProviderConfig {
	main := s.cfg.ResolvedMainModel()
	out := ep.ModelProviderConfig
	if out.Provider == "" {
		out.Provider = "openai"
	}
	out.APIKey, out.APIKeyEnv = ep.ResolvedAPIKey(), ""
	if out.Metadata == nil {
		out.Metadata = main.Metadata
	}
	if out.ReasoningEffort == "" {
		out.ReasoningEffort = main.ReasoningEffort
	}
	if out.StrictTools == nil {
		out.StrictTools = main.StrictTools
	}
	// No max_tokens inheritance: the key exists only on an endpoint, never
	// at the top level, so there is nothing to inherit from.
	return out
}
