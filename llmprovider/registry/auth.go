package registry

import (
	"fmt"
	"strings"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

// ResolveEndpointCredentials keeps provider logins and ambient keys scoped to
// that registered provider's endpoint, including compatible providers. An
// overridden endpoint must supply its own explicit key; do not refresh or read
// vendor credentials for it.
func ResolveEndpointCredentials(store *auth.Store, def provider.Definition, config types.ModelProviderConfig) (auth.Resolved, error) {
	if config.BaseURL != "" && strings.TrimRight(config.BaseURL, "/") != strings.TrimRight(def.BaseURL, "/") {
		if config.APIKey == "" {
			return auth.Resolved{}, fmt.Errorf("custom endpoint requires an explicit API key")
		}
		return auth.Resolved{APIKey: config.APIKey}, nil
	}
	return auth.Resolve(store, def, config.APIKey)
}
