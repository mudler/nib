package endpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mudler/nib/types"
)

// Fingerprint identifies the parts of cfg that decide where a session
// starts, as seen by the saved pick sv. Saved records it at every write, and
// Reconcile compares it with the config at the next start: when they differ,
// the config changed after the pick, and the last change wins.
//
// It covers the default block's provider, base_url, model and api_key_env,
// and, when sv picks a named endpoint, that entry's same fields. The entry's
// model counts only when sv saved a model of its own: a bare pick follows the
// entry's model live, so editing it does not conflict with the pick. Other
// named endpoints are left out, so editing them keeps the pick. API key
// values never go in, not even hashed: api_key_env is the variable's name.
func Fingerprint(cfg types.Config, sv Saved) string {
	var b strings.Builder
	field := func(k, v string) { fmt.Fprintf(&b, "%s=%q\n", k, v) }
	field("default.provider", cfg.Provider)
	field("default.base_url", cfg.BaseURL)
	field("default.model", cfg.Model)
	field("default.api_key_env", cfg.APIKeyEnv)
	if name, ok := strings.CutPrefix(sv.ID, NamedPrefix); ok {
		field("endpoint", name)
		for _, ep := range cfg.Endpoints {
			if ep.Name != name {
				continue
			}
			field("endpoint.provider", ep.Provider)
			field("endpoint.base_url", ep.BaseURL)
			if sv.Model != "" {
				field("endpoint.model", ep.Model)
			}
			field("endpoint.api_key_env", ep.APIKeyEnv)
			break // Validate keeps the first of duplicate names
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Fingerprint is Fingerprint over the config this set was built from.
func (s *Set) Fingerprint(sv Saved) string { return Fingerprint(s.cfg, sv) }

// Reconcile checks a saved pick against the config this set was built from,
// before Startup applies it. It returns the pick to start from, whether
// provider.json must be rewritten with it (a zero Saved means remove it), and
// a note for the boot log.
//
//   - Fingerprint matches: the pick is the last change and sticks.
//   - Fingerprint differs: config.yaml changed after the pick, so the config
//     wins. The pick is dropped, so the note shows once.
//   - No fingerprint (written by an older nib): the pick sticks, and the
//     current fingerprint is backfilled so later edits are detected.
//
// A pick with nothing to override (nothing saved, or a bare pick of the
// default endpoint) and a pick that no longer resolves are left as they are:
// the latter is Startup's to report, with a more precise note.
func (s *Set) Reconcile(sv Saved) (Saved, bool, string) {
	if sv.Model == "" && (sv.ID == "" || sv.ID == DefaultID) {
		return sv, false, ""
	}
	if sv.ID != "" && sv.ID != DefaultID {
		if _, ok := s.Lookup(sv.ID); !ok {
			return sv, false, ""
		}
	}
	current := s.Fingerprint(sv)
	switch sv.ConfigFingerprint {
	case current:
		return sv, false, ""
	case "":
		sv.ConfigFingerprint = current
		return sv, true, ""
	}
	what := sv.ID
	if what == "" {
		what = DefaultID
	}
	if sv.Model != "" {
		what += " · " + sv.Model
	}
	return Saved{}, true, fmt.Sprintf("config.yaml changed since your last endpoint pick (%s); starting on config.yaml", what)
}
