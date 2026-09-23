// Package classify is nib's interface to small classification models: a
// model that answers structured questions about a text instead of
// generating one. The approval_mode "classify" gate and the TUI's reply
// suggestions are built on it. Backends live in subpackages.
package classify

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/types"
)

// Question types.
const (
	TypeNoul   = "noul"   // is the described thing present, 0–1, with entity spans
	TypeChoice = "choice" // pick one of Choices
	TypeScore  = "score"  // pick one of Levels
)

// Question is one question asked about a text.
type Question struct {
	Type         string
	Instructions string
	Choices      map[string]string // choice: option name → description (may be empty)
	Levels       []string          // score: level descriptions
}

// Entity is a span of the text a noul question found.
type Entity struct {
	Text       string
	Start, End int
	Confidence float64
}

// Answer is one question's answer. Which fields are set depends on the type.
type Answer struct {
	Noul          float64            // noul
	Entities      []Entity           // noul
	Choice        string             // choice
	Confidence    float64            // choice, score
	Probabilities map[string]float64 // choice, score
	Score         float64            // score
}

// Classifier answers questions about a text. Answers are keyed like qs.
type Classifier interface {
	Classify(ctx context.Context, state string, qs map[string]Question) (map[string]Answer, error)
}

// Categories are the risk categories a tool call is classified into.
var Categories = []string{"inspect", "build_test", "local_edit", "destructive", "network", "system"}

// ValidCategory reports whether name is one of Categories.
func ValidCategory(name string) bool { return slices.Contains(Categories, name) }

// DefaultTimeout bounds a request when classifier.timeout is unset.
const DefaultTimeout = 2 * time.Second

// Builder makes a Classifier for one API. Subpackages register themselves
// so this package does not import them.
type Builder func(baseURL, apiKey, model string, timeout time.Duration) Classifier

var builders = map[string]Builder{}

// Register makes an API name available to New.
func Register(api string, b Builder) { builders[api] = b }

// Resolve returns the base URL and API key the classifier talks to: the
// named endpoint's, or the top-level ones when classifier.endpoint is empty.
func Resolve(cfg types.Config) (baseURL, apiKey string, err error) {
	if name := cfg.Classifier.Endpoint; name != "" {
		set, _ := endpoint.New(cfg, nil)
		p, err := set.Config(endpoint.NamedPrefix + name)
		if err != nil {
			return "", "", fmt.Errorf("classifier: %w", err)
		}
		return p.BaseURL, p.APIKey, nil
	}
	m := cfg.ResolvedMainModel()
	return m.BaseURL, m.APIKey, nil
}

// New builds the configured classifier, or returns nil, nil when none is
// configured.
func New(cfg types.Config) (Classifier, error) {
	c := cfg.Classifier
	if !c.Configured() {
		return nil, nil
	}
	api := c.API
	if api == "" {
		api = "systemone"
	}
	b, ok := builders[api]
	if !ok {
		return nil, fmt.Errorf("classifier: unknown api %q", api)
	}
	base, key, err := Resolve(cfg)
	if err != nil {
		return nil, err
	}
	if base == "" {
		return nil, fmt.Errorf("classifier: no base_url to reach it")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return b(base, key, c.Model, timeout), nil
}
