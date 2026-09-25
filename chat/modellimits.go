package chat

import (
	"context"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider"
	"github.com/mudler/nib/llmprovider/catalog"
	"github.com/mudler/nib/types"
)

// A model has two limits nib can only learn by asking the endpoint: the
// context window it accepts, and the output cap it allows. Both answers come
// from an HTTP round-trip, and both used to be fetched while the session was
// being built — one probe inside NewSession, one inside the LLM factory.
//
// That cost every start up to two blocking requests before the user had typed
// anything, made a model switch fail on a machine with no network, and moved
// the endpoint's request counter underneath any test that scripts replies by
// request number. The probes belong where their answers are first needed: the
// turn that is about to be sent.
//
// ensureModelLimits runs them once per model, at the start of a turn. It is
// best-effort by design: an endpoint that cannot answer leaves the defaults in
// place, exactly as a failed probe did before.
func (s *Session) ensureModelLimits(ctx context.Context) {
	s.modelMu.Lock()
	model, provider := s.llmModel, s.mainProvider
	done := s.limitsFor == model && model != ""
	llm := s.llm
	s.modelMu.Unlock()
	if done || model == "" {
		return
	}

	baseURL, apiKey, _ := llmprovider.ModelsEndpoint(provider, s.credStore)

	// The context window: only when the user did not name one. An explicit
	// setting is never overwritten by a probe.
	if s.compactionAutoDetected {
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		if v := detectContextSize(probeCtx, baseURL, apiKey, model); v > 0 {
			s.modelMu.Lock()
			s.compaction.MaxContextTokens = v
			s.modelMu.Unlock()
		}
		cancel()
	}

	// The output cap: discovery only, since config and catalog were already
	// consulted without a request when the client was built.
	if setter, ok := llm.(maxTokensSetter); ok {
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		res := catalog.ResolveMaxTokens(probeCtx, provider, baseURL, apiKey)
		cancel()
		if !res.Omit && res.MaxTokens > 0 {
			setter.SetMaxTokens(res.MaxTokens)
			s.modelMu.Lock()
			if s.llmModel == model {
				s.outputCap = res.MaxTokens
			}
			s.modelMu.Unlock()
		}
	}

	s.modelMu.Lock()
	s.limitsFor = model
	s.modelMu.Unlock()
}

// maxTokensSetter is the part of the LLM client that accepts a cap resolved
// after the client was built. Adapters that do not carry an output cap simply
// do not implement it.
type maxTokensSetter interface {
	SetMaxTokens(int)
}

// factoryOutputCap is the output cap the LLM factory gave llm when it built it
// for provider: the same resolution (config, then catalog, no network), so the
// session knows what each request reserves without asking the client. A
// client that carries no cap (no SetMaxTokens) reports 0, which leaves its
// requests unclamped.
func factoryOutputCap(llm cogito.LLM, provider types.ModelProviderConfig, store *auth.Store) int {
	if _, ok := llm.(maxTokensSetter); !ok {
		return 0
	}
	baseURL, apiKey, _ := llmprovider.ModelsEndpoint(provider, store)
	res := catalog.ResolveMaxTokens(nil, provider, baseURL, apiKey)
	if res.Omit || res.MaxTokens <= 0 {
		return 0
	}
	return res.MaxTokens
}

// requestLimits reports the output cap and context window the turn's requests
// are clamped against (see clampOutputTokens), read under one lock so a model
// switch cannot pair one model's cap with another's window.
func (s *Session) requestLimits() (cap, window int) {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.outputCap, s.windowLocked()
}
