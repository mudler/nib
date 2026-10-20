package auth

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mudler/nib/auth/oauth"
	"github.com/mudler/nib/provider"
)

// Resolved is the output of the credential resolution ladder: the key or
// token to present on the wire, and whether it is an OAuth bearer token (which
// changes the headers the adapter sends).
type Resolved struct {
	// APIKey is the credential: an API key for CredentialAPIKey creds, or an
	// OAuth access token for CredentialOAuth creds. Empty if nothing resolved.
	APIKey string
	// IsOAuth is true when APIKey is an OAuth access token. The adapter uses
	// this to pick Authorization: Bearer + anthropic-beta vs x-api-key.
	IsOAuth bool
}

// Resolve runs the credential resolution ladder for a provider:
//
//  1. Compatible stored credential from /login (OAuth with refresh, or API key)
//  2. configAPIKey (from types.Config.APIKey — the existing YAML/env path)
//  3. Environment variable (def.EnvVar — e.g. ANTHROPIC_API_KEY)
//  4. Fallback (empty Resolved)
//
// A stored OAuth credential whose access token is about to expire is
// refreshed in-place and persisted back to the store before returning. If the
// refresh fails, the resolver falls through to configAPIKey / env rather than
// returning an error — a stale OAuth token is worse than a working API key.
func Resolve(store *Store, def provider.Definition, configAPIKey string) (Resolved, error) {
	// 1. Stored credential
	if store != nil {
		cred, ok, err := store.Get(def.ID)
		// API-key-only providers skip incompatible credentials before any OAuth
		// refresh. Other login kinds retain their existing credential behavior.
		if err == nil && ok && (def.LoginKind != provider.LoginAPIKey || cred.Kind == CredentialAPIKey) {
			if cred.Kind == CredentialOAuth {
				r, _ := resolveOAuth(store, def, cred)
				if r.APIKey != "" {
					return r, nil
				}
				// Refresh failed or token is empty — fall through to
				// config/env rather than returning empty.
			}
			if cred.Kind == CredentialAPIKey && cred.APIKey != "" {
				return Resolved{APIKey: cred.APIKey, IsOAuth: false}, nil
			}
		}
		// A store error is non-fatal: fall through to other sources.
	}

	// 2. Config API key
	if configAPIKey != "" {
		return Resolved{APIKey: configAPIKey, IsOAuth: false}, nil
	}

	// 3. Environment variable
	if def.EnvVar != "" {
		if v := os.Getenv(def.EnvVar); v != "" {
			return Resolved{APIKey: v, IsOAuth: false}, nil
		}
	}

	// 4. Fallback
	return Resolved{}, nil
}

// resolveOAuth handles a stored OAuth credential, refreshing it if needed.
func resolveOAuth(store *Store, def provider.Definition, cred Credential) (Resolved, error) {
	if cred.NeedsRefresh() && cred.RefreshToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tr, err := oauth.Refresh(ctx, def, cred.RefreshToken)
		if err != nil {
			// Refresh failed. Fall through to other sources rather than
			// returning an error — the caller may have a config/env key.
			return Resolved{}, nil
		}

		cred.AccessToken = tr.AccessToken
		if tr.RefreshToken != "" {
			cred.RefreshToken = tr.RefreshToken
		}
		cred.ExpiresAt = oauth.ExpiresAt(tr.ExpiresIn)
		// Persist the refreshed token. A save failure is non-fatal — the
		// in-memory credential is still valid for this session.
		_ = store.Save(cred)
	}

	if cred.AccessToken == "" {
		return Resolved{}, nil
	}
	return Resolved{APIKey: cred.AccessToken, IsOAuth: true}, nil
}

// ResolveOrError is like Resolve but returns an error when no credential is
// found, for call sites that require auth (no anonymous access).
func ResolveOrError(store *Store, def provider.Definition, configAPIKey string) (Resolved, error) {
	r, err := Resolve(store, def, configAPIKey)
	if err != nil {
		return r, err
	}
	if r.APIKey == "" {
		return r, fmt.Errorf("auth: no credentials for %s — run 'nib login %s' or set %s", def.ID, def.ID, def.EnvVar)
	}
	return r, nil
}
