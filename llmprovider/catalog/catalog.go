package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// To reproduce the embedded catalog from pinned provenance, run:
//
//	go generate ./llmprovider/catalog/...
//
// This verifies the pinned upstream bytes and compresses them deterministically.
// To refresh, run go run gen_catalog.go -refresh from this directory, then
// review and commit both catalog.json.zst and provenance.json.
//
//go:generate go run gen_catalog.go
//go:embed catalog.json.zst
var compressedCatalog []byte

var (
	loadOnce  sync.Once
	loadErr   error
	catalogDB map[string]map[string]Model // provider -> modelID -> Model

	// Secondary indices built on load. Model IDs keep their catalog case
	// ("zai-org/GLM-5.2") while users type them in any case, so the ID keys
	// below are lowercased, and each bucket holds every entry whose ID folds
	// to that key. One provider can list IDs that differ only by case, so a
	// provider bucket can hold more than one entry.
	byProviderModel map[string]map[string][]Model // provider -> lowercased modelID -> models
	byModelID       map[string][]Model            // lowercased modelID -> models
	byBaseURLHost   map[string][]Model            // host -> models
	modelIDs        []string                      // byModelID's keys, sorted
)

// Load decompresses and parses the embedded catalog. It is called lazily by
// Lookup and is safe to call from multiple goroutines.
func Load() error {
	loadOnce.Do(func() {
		raw, err := decompress(compressedCatalog)
		if err != nil {
			loadErr = fmt.Errorf("catalog: decompress: %w", err)
			return
		}
		if err := json.Unmarshal(raw, &catalogDB); err != nil {
			loadErr = fmt.Errorf("catalog: parse: %w", err)
			return
		}
		buildIndices()
	})
	return loadErr
}

func decompress(data []byte) ([]byte, error) {
	r, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.DecodeAll(data, nil)
}

// buildIndices walks providers and their models in sorted order. The same
// model ID is often served by several providers with different limits, and
// Lookup returns the first acceptable entry of a bucket, so the bucket order
// decides the answer. Built from map iteration, that order changed from run
// to run, and so did the max_tokens nib sent for a model such as glm-5.2.
func buildIndices() {
	byProviderModel = make(map[string]map[string][]Model)
	byModelID = make(map[string][]Model)
	byBaseURLHost = make(map[string][]Model)
	for _, provider := range sortedKeys(catalogDB) {
		models := catalogDB[provider]
		byID := make(map[string][]Model, len(models))
		byProviderModel[strings.ToLower(provider)] = byID
		for _, id := range sortedKeys(models) {
			m := models[id]
			key := strings.ToLower(m.ID)
			byID[key] = append(byID[key], m)
			byModelID[key] = append(byModelID[key], m)
			if h := hostOf(m.BaseURL); h != "" {
				byBaseURLHost[h] = append(byBaseURLHost[h], m)
			}
		}
	}
	modelIDs = sortedKeys(byModelID)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Lookup finds a model in the catalog using progressively looser matching:
//  1. Exact provider + model ID
//  2. Base URL host + exact model ID (provider-specific via endpoint)
//  3. Model ID across all providers (prefers non-openrouter entries)
//  4. Model ID suffix match (strip "org/" prefix from catalog IDs)
//
// Returns nil, false when nothing matches.
func Lookup(providerName, modelID, baseURL string) (*Model, bool) {
	if err := Load(); err != nil {
		return nil, false
	}
	providerName = strings.ToLower(providerName)
	key := strings.ToLower(modelID)

	// 1. Exact provider + model ID.
	if matches := byProviderModel[providerName][key]; len(matches) > 0 {
		return &matches[preferExactCase(matches, modelID)], true
	}

	// 2. Base URL host + exact model ID — more precise than a bare
	//    model-ID search because it ties the match to the actual endpoint.
	host := hostOf(baseURL)
	if host != "" {
		matches := byBaseURLHost[host]
		best := -1
		for i := range matches {
			if matches[i].ID == modelID {
				return &matches[i], true
			}
			if best < 0 && strings.EqualFold(matches[i].ID, modelID) {
				best = i
			}
		}
		if best >= 0 {
			return &matches[best], true
		}
	}

	// 3. Model ID across all providers. When the same model ID is served
	//    by multiple providers, prefer a non-openrouter entry (openrouter
	//    has the omit flag, which would suppress max_tokens even for a
	//    direct provider that needs it).
	//    Among those, prefer the entry in the case the user typed.
	if matches := byModelID[key]; len(matches) > 0 {
		var direct []Model
		for _, m := range matches {
			if !m.Compat.IsOpenRouterHost {
				direct = append(direct, m)
			}
		}
		if len(direct) > 0 {
			matches = direct
		}
		return &matches[preferExactCase(matches, modelID)], true
	}

	// 4. Suffix match: strip "org/" prefix from catalog IDs.
	//    e.g. user model "glm-5.2" matches catalog ID "zai-org/glm-5.2".
	for _, id := range modelIDs {
		matches := byModelID[id]
		for i := range matches {
			if suffixMatch(matches[i].ID, key) {
				return &matches[i], true
			}
		}
	}

	return nil, false
}

// preferExactCase returns the index of the entry whose ID is exactly modelID,
// or 0 when every entry differs from it only by case.
func preferExactCase(matches []Model, modelID string) int {
	for i := range matches {
		if matches[i].ID == modelID {
			return i
		}
	}
	return 0
}

// suffixMatch reports whether catalogID ends with modelID after stripping an
// optional "org/" prefix from catalogID. e.g. "zai-org/glm-5.2" matches "glm-5.2".
func suffixMatch(catalogID, modelID string) bool {
	catalogID = strings.ToLower(catalogID)
	if idx := strings.LastIndex(catalogID, "/"); idx >= 0 {
		catalogID = catalogID[idx+1:]
	}
	return catalogID == modelID
}

func hostOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// Stats returns the number of providers and models in the catalog.
func Stats() (providers, models int) {
	if err := Load(); err != nil {
		return 0, 0
	}
	providers = len(catalogDB)
	for _, m := range catalogDB {
		models += len(m)
	}
	return
}
