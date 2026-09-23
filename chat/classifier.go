package chat

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/nib/classify"
	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/types"
)

// classifierState is everything built on the small classification model
// (classifier:). The session holds it behind an atomic pointer, so /settings
// and /classifier can replace it while a turn reads it. Nil means none.
type classifierState struct {
	approver     *Approver
	suggester    Suggester
	suggestDelay time.Duration
	info         string // what /classifier reports, e.g. "gliner @ home"
}

// buildClassifier makes the classifier state cfg describes, or nil, nil when
// cfg configures none.
func buildClassifier(cfg types.Config) (*classifierState, error) {
	c, err := classify.New(cfg)
	if err != nil || c == nil {
		return nil, err
	}
	st := &classifierState{
		approver: NewApprover(c, cfg.AutoApprove, cfg.WorkingDir),
		info:     classifierInfo(cfg.Classifier),
	}
	if !cfg.Suggestions.Disabled {
		st.suggester = NewClassifierSuggester(c, cfg.Suggestions)
		st.suggestDelay = cfg.Suggestions.Delay
	}
	return st, nil
}

func classifierInfo(c types.ClassifierConfig) string {
	where := endpoint.DefaultName
	if c.Endpoint != "" {
		where = c.Endpoint
	}
	if c.Model == "" {
		return where
	}
	return c.Model + " @ " + where
}

// validateAutoApprove reports auto_approve.allow entries that are not
// categories.
func validateAutoApprove(cfg types.AutoApproveConfig) error {
	for _, name := range cfg.Allow {
		if !classify.ValidCategory(name) {
			return fmt.Errorf("auto_approve.allow: unknown category %q (known: %s)", name, strings.Join(classify.Categories, ", "))
		}
	}
	return nil
}

func (s *Session) classifier() *classifierState { return s.cls.Load() }

// setClassifierState installs st (nil removes the classifier). Leaving
// classify mode without a classifier falls back to prompt; fellBack reports
// that.
func (s *Session) setClassifierState(st *classifierState) (fellBack bool) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	s.cls.Store(st)
	if st == nil && s.approvalMode == types.ApprovalClassify {
		s.approvalMode = types.ApprovalPrompt
		return true
	}
	return false
}

// SetClassifier replaces the classifier with the one cfg describes, for the
// rest of the session: its classifier, auto_approve and suggestions blocks.
// A cfg with no classifier removes it; in classify mode the session then
// falls back to prompt, and fellBack is true. On an error the current
// classifier stays.
func (s *Session) SetClassifier(cfg types.Config) (fellBack bool, err error) {
	if err := validateAutoApprove(cfg.AutoApprove); err != nil {
		return false, err
	}
	st, err := buildClassifier(cfg)
	if err != nil {
		return false, err
	}
	return s.setClassifierState(st), nil
}

// ClassifierInfo names the classifier in use ("model @ endpoint"), or "".
func (s *Session) ClassifierInfo() string {
	if st := s.classifier(); st != nil {
		return st.info
	}
	return ""
}

// ErrClassifierNeedsModel refuses a classifier on config.yaml's own
// endpoint with no model: a block with neither field means "none".
var ErrClassifierNeedsModel = errors.New("name a model for the config.yaml endpoint: /classifier config <model>")

// ClassifierChoice is cfg's classifier block pointed at endpointID (a picker
// ID such as "@home" or "config", or a bare endpoint name) and model. Its
// other fields (api, timeout) carry over.
func ClassifierChoice(cfg types.ClassifierConfig, endpointID, model string) (types.ClassifierConfig, error) {
	name := strings.TrimPrefix(endpointID, endpoint.NamedPrefix)
	if endpointID == endpoint.DefaultID || endpointID == endpoint.DefaultName {
		name = ""
	}
	cfg.Endpoint, cfg.Model = name, model
	if !cfg.Configured() {
		return cfg, ErrClassifierNeedsModel
	}
	return cfg, nil
}
