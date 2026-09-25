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
// switch cannot pair one model's cap with another's window. A one-turn
// override (turnOutputCap) lowers the cap, and never raises it.
func (s *Session) requestLimits() (cap, window int) {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	cap = s.outputCap
	if o := s.turnOutputCap; o > 0 && (cap <= 0 || o < cap) {
		cap = o
	}
	return cap, s.windowLocked()
}

// setTurnOutputCap sets (or, with 0, clears) the current turn's output cap
// override.
func (s *Session) setTurnOutputCap(v int) {
	s.modelMu.Lock()
	s.turnOutputCap = v
	s.modelMu.Unlock()
}

// lowerOutputCap lowers the output cap of model to max after the backend
// said the requested output is above what it allows. It never raises the
// cap. It reports whether the cap changed, so the caller retries only a
// request that will differ. The client's own cap is lowered too, because it
// is what a request carries when the window is unknown and nothing clamps.
func (s *Session) lowerOutputCap(max int, model string) bool {
	if max <= 0 {
		return false
	}
	s.modelMu.Lock()
	if s.llmModel != model || (s.outputCap > 0 && s.outputCap <= max) {
		s.modelMu.Unlock()
		return false
	}
	s.outputCap = max
	llm := s.llm
	s.modelMu.Unlock()
	if setter, ok := llm.(maxTokensSetter); ok {
		setter.SetMaxTokens(max)
	}
	return true
}

// budgetRetryOutput is the output reservation that fits a budget overflow:
// the window less the input the backend counted, less outputSafetyMargin. The
// backend's count is exact, which nib's estimate is not. It reports false when
// the figures are missing or the result is below minOutputTokens: then the
// prompt itself leaves no useful room, and only a smaller prompt helps.
func budgetRetryOutput(info overflowInfo) (int, bool) {
	input := info.Input
	if input <= 0 && info.Total > 0 && info.Output > 0 {
		input = info.Total - info.Output
	}
	if info.Window <= 0 || input <= 0 {
		return 0, false
	}
	out := info.Window - input - outputSafetyMargin
	if out < minOutputTokens {
		return 0, false
	}
	return out, true
}
