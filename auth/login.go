package auth

import (
	"context"
	"fmt"
	neturl "net/url"
	"os"
	osuser "os/user"
	"strings"
	"time"

	"github.com/mudler/nib/auth/oauth"
	"github.com/mudler/nib/provider"
)

// LoginAPIKey creates and stores an API-key credential for a provider.
// Returns the credential (with AuthorizedAt set).
func LoginAPIKey(store *Store, def provider.Definition, apiKey string) (Credential, error) {
	return LoginAPIKeyAt(store, def, apiKey, "")
}

// LoginAPIKeyAt is LoginAPIKey with an endpoint override stored alongside the
// key, for providers without a default base URL (see
// provider.Definition.NeedsBaseURL). An empty baseURL keeps the default.
func LoginAPIKeyAt(store *Store, def provider.Definition, apiKey, baseURL string) (Credential, error) {
	if apiKey == "" {
		return Credential{}, fmt.Errorf("auth: empty API key for %s", def.ID)
	}
	cred := Credential{
		ProviderID: def.ID,
		Kind:       CredentialAPIKey,
		APIKey:     apiKey,
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
	}
	if err := store.Save(cred); err != nil {
		return Credential{}, fmt.Errorf("auth: save %s credential: %w", def.ID, err)
	}
	return cred, nil
}

// OAuthFlow holds the state of an in-progress OAuth authorization-code flow:
// the PKCE verifier, the CSRF state, and the running callback server. Start
// with StartOAuthFlow, display AuthorizeURL to the user, then call Complete
// to wait for the callback and exchange the code.
type OAuthFlow struct {
	def         provider.Definition
	verifier    string
	state       string
	server      *oauth.CallbackServer
	redirectURI string
}

// StartOAuthFlow generates PKCE parameters, starts the loopback callback
// server, and returns a flow ready to display the authorize URL.
func StartOAuthFlow(def provider.Definition) (*OAuthFlow, error) {
	verifier, err := oauth.GenerateVerifier()
	if err != nil {
		return nil, err
	}
	state, err := oauth.GenerateState()
	if err != nil {
		return nil, err
	}
	server := oauth.NewCallbackServer(def.CallbackPort, def.CallbackPath, def.CallbackHost, state)
	redirectURI, err := server.Start()
	if err != nil {
		return nil, fmt.Errorf("auth: start callback server: %w", err)
	}
	return &OAuthFlow{
		def:         def,
		verifier:    verifier,
		state:       state,
		server:      server,
		redirectURI: redirectURI,
	}, nil
}

// AuthorizeURL returns the URL the user must open to authorize the app.
func (f *OAuthFlow) AuthorizeURL() string {
	challenge := oauth.GenerateChallenge(f.verifier)
	return oauth.AuthorizeURL(f.def, challenge, f.state, f.redirectURI)
}

// RedirectHost returns the host of the redirect URI the provider sends the
// browser to: the provider's callback host, "localhost" when it sets none.
func (f *OAuthFlow) RedirectHost() string {
	if u, err := neturl.Parse(f.redirectURI); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "localhost"
}

// ManualCallback parses a pasted callback URL and delivers it to the
// callback server. This is the fallback for SSH sessions where the
// browser's redirect to localhost:port cannot reach the machine running
// nib. The user copies the full URL from the browser's address bar (the
// redirect URL that failed to connect) and pastes it here.
func (f *OAuthFlow) ManualCallback(rawURL string) error {
	return f.server.ManualCallback(rawURL)
}

// CallbackPort returns the port the callback server is listening on.
func (f *OAuthFlow) CallbackPort() int {
	return f.server.Port()
}

// Complete waits for the OAuth callback, exchanges the code for tokens,
// bootstraps identity, and returns a credential ready to save. The caller
// is responsible for persisting it via Store.Save.
func (f *OAuthFlow) Complete(ctx context.Context) (Credential, error) {
	result := f.server.Wait(ctx)
	if result.Err != nil {
		return Credential{}, fmt.Errorf("auth: oauth callback: %w", result.Err)
	}

	tr, err := oauth.Exchange(ctx, f.def, result.Code, f.verifier, f.redirectURI, result.State)
	if err != nil {
		return Credential{}, fmt.Errorf("auth: exchange code: %w", err)
	}

	// Identity recovery is best-effort: the token is valid even if we
	// cannot recover the account/org info. omp does the same.
	cred := Credential{
		ProviderID:   f.def.ID,
		Kind:         CredentialOAuth,
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    oauth.ExpiresAt(tr.ExpiresIn),
	}
	if id, err := oauth.FetchIdentity(ctx, f.def, tr.AccessToken); err == nil {
		cred.AccountID = id.AccountID
		cred.Email = id.Email
		cred.OrgID = id.OrgID
		cred.OrgName = id.OrgName
	}

	// Post-exchange hook (e.g., Cloud Code Assist project discovery).
	if hook := GetPostExchange(f.def.ID); hook != nil {
		var err error
		cred, err = hook(ctx, cred, f.def)
		if err != nil {
			return Credential{}, fmt.Errorf("auth: post-exchange hook for %s: %w", f.def.ID, err)
		}
	}

	return cred, nil
}

// LoginOAuth runs the full OAuth flow (start + complete) and saves the
// credential. onURL is called with the authorize URL before waiting for the
// callback, so the caller can display it or open a browser.
func LoginOAuth(ctx context.Context, store *Store, def provider.Definition, onURL func(string)) (Credential, error) {
	flow, err := StartOAuthFlow(def)
	if err != nil {
		return Credential{}, err
	}
	onURL(flow.AuthorizeURL())
	cred, err := flow.Complete(ctx)
	if err != nil {
		return Credential{}, err
	}
	if err := store.Save(cred); err != nil {
		return Credential{}, fmt.Errorf("auth: save %s credential: %w", def.ID, err)
	}
	return cred, nil
}

// LoginDeviceCode runs the RFC 8628 device-code flow and saves the credential.
// onInstructions is called with a human-readable instruction string (containing
// the user code and verification URL) before polling begins, so the caller can
// display it and optionally open the verification URL in a browser.
func LoginDeviceCode(ctx context.Context, store *Store, def provider.Definition, onInstructions func(string, string)) (Credential, error) {
	dr, err := oauth.RequestDeviceCode(ctx, def)
	if err != nil {
		return Credential{}, fmt.Errorf("auth: device code: %w", err)
	}

	// Build the instruction string and the URL to open.
	verificationURL := dr.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = dr.VerificationURI
	}
	instructions := fmt.Sprintf("Go to %s and enter code: %s", dr.VerificationURI, dr.UserCode)
	onInstructions(instructions, verificationURL)

	tr, err := oauth.PollDeviceToken(ctx, def, dr)
	if err != nil {
		return Credential{}, fmt.Errorf("auth: device flow: %w", err)
	}

	cred := Credential{
		ProviderID:   def.ID,
		Kind:         CredentialOAuth,
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    oauth.ExpiresAt(tr.ExpiresIn),
	}
	if id, err := oauth.FetchIdentity(ctx, def, tr.AccessToken); err == nil {
		cred.AccountID = id.AccountID
		cred.Email = id.Email
		cred.OrgID = id.OrgID
		cred.OrgName = id.OrgName
	}

	// Post-exchange hook (e.g., Cloud Code Assist project discovery).
	if hook := GetPostExchange(def.ID); hook != nil {
		var err error
		cred, err = hook(ctx, cred, def)
		if err != nil {
			return Credential{}, fmt.Errorf("auth: post-exchange hook for %s: %w", def.ID, err)
		}
	}

	if err := store.Save(cred); err != nil {
		return Credential{}, fmt.Errorf("auth: save %s credential: %w", def.ID, err)
	}
	return cred, nil
}

// LoginFlow represents an in-progress login flow. The caller displays Prompt
// to the user (and optionally opens URL in a browser), then calls Complete to
// finish the flow asynchronously.
// LoginFlow carries the state needed to finish an asynchronous login
// (OAuth-code or device-code) after StartLogin returns. The caller
// displays Prompt, opens URL in a browser (unless IsSSH), then calls
// Complete to wait for the result.
type LoginFlow struct {
	ProviderID string
	Prompt     string // authorize URL or device-code instructions
	URL        string // URL to open in a browser (may differ from Prompt)
	IsSSH      bool   // true when the session appears to be over SSH
	// SSHPortForward is a pre-formatted port-forwarding hint (empty when
	// not over SSH or when the flow doesn't use a localhost callback).
	SSHPortForward string
	complete       func(context.Context) (Credential, error)
	manualCB       func(string) error // feeds a pasted callback URL into the OAuth flow
}

// ManualCallback feeds a pasted callback URL into the OAuth flow: the
// fallback for when the browser redirect cannot reach the callback server.
// That happens over SSH, but also when the browser runs on another machine
// or in a sandbox, or a firewall blocks the port. It returns an error when
// the flow does not support it (device-code flows, Copilot token import);
// see AcceptsPastedURL.
func (f *LoginFlow) ManualCallback(rawURL string) error {
	if f.manualCB == nil {
		return fmt.Errorf("this login flow does not support manual callback")
	}
	return f.manualCB(rawURL)
}

// AcceptsPastedURL reports whether ManualCallback can finish this flow, so a
// caller knows whether to offer the user a place to paste the redirect URL.
func (f *LoginFlow) AcceptsPastedURL() bool {
	return f.manualCB != nil
}

// Complete finishes the login flow, returning the saved credential. It blocks
// until the OAuth callback arrives, the device-code poll succeeds, or the
// context is cancelled.
func (f *LoginFlow) Complete(ctx context.Context) (Credential, error) {
	return f.complete(ctx)
}

// NewLoginFlow constructs a LoginFlow with the given display fields and
// completion function. It is intended for providers that finish the flow
// synchronously (e.g. Copilot token import) and need a no-op Complete.
func NewLoginFlow(providerID, prompt, url string, complete func(context.Context) (Credential, error)) *LoginFlow {
	return &LoginFlow{
		ProviderID: providerID,
		Prompt:     prompt,
		URL:        url,
		complete:   complete,
	}
}

// StartLogin begins an OAuth-code or device-code login flow for def. The
// returned LoginFlow's Prompt should be displayed to the user and URL opened
// in a browser; then Complete should be called to finish the flow.
//
// When the session is over SSH, StartLogin auto-switches to the device-code
// flow if the provider supports it (DeviceURL is set). For OAuth-code
// providers without device flow, the returned LoginFlow carries a
// port-forwarding hint in SSHPortForward and a ManualCallback that lets the
// user paste the callback URL.
func StartLogin(ctx context.Context, store *Store, def provider.Definition) (*LoginFlow, error) {
	ssh := isSSHSession()

	// Over SSH, auto-switch to device-code flow when the provider supports it.
	if ssh && def.LoginKind == provider.LoginOAuthCode && def.DeviceURL != "" {
		def.LoginKind = provider.LoginDeviceCode
	}

	switch def.LoginKind {
	case provider.LoginOAuthCode:
		return startOAuthLogin(ctx, store, def, ssh)
	case provider.LoginDeviceCode:
		return startDeviceLogin(ctx, store, def)
	default:
		return nil, fmt.Errorf("auth: StartLogin does not support login kind %q for %s", def.LoginKind, def.ID)
	}
}

// startOAuthLogin starts a localhost-callback OAuth flow. When ssh is true,
// the returned LoginFlow includes a port-forwarding hint and a manual
// callback so the user can paste the redirect URL.
func startOAuthLogin(ctx context.Context, store *Store, def provider.Definition, ssh bool) (*LoginFlow, error) {
	flow, err := StartOAuthFlow(def)
	if err != nil {
		return nil, err
	}
	url := flow.AuthorizeURL()
	port := flow.CallbackPort()

	lf := &LoginFlow{
		ProviderID: def.ID,
		URL:        url,
		complete: func(ctx context.Context) (Credential, error) {
			cred, err := flow.Complete(ctx)
			if err != nil {
				return Credential{}, err
			}
			if err := store.Save(cred); err != nil {
				return Credential{}, fmt.Errorf("auth: save %s credential: %w", def.ID, err)
			}
			return cred, nil
		},
		manualCB: flow.ManualCallback,
	}

	if ssh {
		lf.IsSSH = true
		lf.SSHPortForward = sshPortForwardHint(port)
		lf.Prompt = sshOAuthPrompt(url, lf.SSHPortForward, flow.RedirectHost(), port)
	} else {
		lf.Prompt = localOAuthPrompt(url)
	}
	return lf, nil
}

// startDeviceLogin starts an RFC 8628 device-code flow.
func startDeviceLogin(ctx context.Context, store *Store, def provider.Definition) (*LoginFlow, error) {
	dr, err := oauth.RequestDeviceCode(ctx, def)
	if err != nil {
		return nil, fmt.Errorf("auth: device code: %w", err)
	}
	verificationURL := dr.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = dr.VerificationURI
	}
	instructions := fmt.Sprintf("Go to %s and enter code: %s", dr.VerificationURI, dr.UserCode)
	return &LoginFlow{
		ProviderID: def.ID,
		Prompt:     instructions,
		URL:        verificationURL,
		complete: func(ctx context.Context) (Credential, error) {
			tr, err := oauth.PollDeviceToken(ctx, def, dr)
			if err != nil {
				return Credential{}, fmt.Errorf("auth: device flow: %w", err)
			}
			cred := Credential{
				ProviderID:   def.ID,
				Kind:         CredentialOAuth,
				AccessToken:  tr.AccessToken,
				RefreshToken: tr.RefreshToken,
				ExpiresAt:    oauth.ExpiresAt(tr.ExpiresIn),
			}
			if id, err := oauth.FetchIdentity(ctx, def, tr.AccessToken); err == nil {
				cred.AccountID = id.AccountID
				cred.Email = id.Email
				cred.OrgID = id.OrgID
				cred.OrgName = id.OrgName
			}
			if hook := GetPostExchange(def.ID); hook != nil {
				var err error
				cred, err = hook(ctx, cred, def)
				if err != nil {
					return Credential{}, fmt.Errorf("auth: post-exchange hook for %s: %w", def.ID, err)
				}
			}
			if err := store.Save(cred); err != nil {
				return Credential{}, fmt.Errorf("auth: save %s credential: %w", def.ID, err)
			}
			return cred, nil
		},
	}, nil
}

// --- SSH detection helpers ---

// isSSHSession reports whether the current session appears to be over SSH.
// It checks for SSH_CONNECTION (set by sshd for every session) and SSH_TTY
// (set for interactive sessions). Either being non-empty is a strong signal
// that the user's browser is on a different machine than the one running nib.
func isSSHSession() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
}

// sshUserHost returns the user@host to put in an ssh -L forwarding hint.
//
// The host is the server address from SSH_CONNECTION ("client_ip
// client_port server_ip server_port"), the address the user's ssh already
// reached, and otherwise this machine's hostname. SSH_CONNECTION is often
// missing even over SSH: a shell inside tmux or sudo keeps SSH_TTY but not
// always SSH_CONNECTION. The parts that cannot be found become readable
// placeholders, never an empty string, so the hint never shows a bare "@".
func sshUserHost() string {
	user := os.Getenv("USER")
	if user == "" {
		if u, err := osuser.Current(); err == nil {
			user = u.Username
		}
	}
	if user == "" {
		user = "<user>"
	}
	host := ""
	if f := strings.Fields(os.Getenv("SSH_CONNECTION")); len(f) >= 3 {
		host = f[2]
	}
	if host == "" {
		if h, err := os.Hostname(); err == nil {
			host = h
		}
	}
	if host == "" {
		host = "<this-host>"
	}
	return user + "@" + host
}

// sshPortForwardHint returns the "ssh -L port:127.0.0.1:port user@host"
// command that carries the browser's redirect to the callback server. The
// forward targets 127.0.0.1 because that is the address the callback server
// binds (oauth.CallbackServer.Start). "localhost" on the remote side can
// resolve to ::1 first, where nothing listens.
func sshPortForwardHint(port int) string {
	return fmt.Sprintf("ssh -L %d:127.0.0.1:%d %s", port, port, sshUserHost())
}

// localOAuthPrompt builds the prompt shown for an OAuth-code flow outside
// SSH: the authorize URL, and the paste-URL fallback for a browser whose
// redirect cannot reach this machine.
func localOAuthPrompt(url string) string {
	return "Open this URL to log in:\n" + url + "\n\n" +
		"If the browser cannot connect after you log in, copy the full URL\n" +
		"from its address bar and paste it here."
}

// sshOAuthPrompt builds the multi-line prompt shown for an OAuth-code flow
// over SSH: port-forward command, the authorize URL, and paste-URL fallback
// instructions.
//
// redirectHost is the host the browser is redirected to (the provider's
// callback host, such as 127.0.0.1 for openai-codex), so the text names the
// address the user will actually see fail.
func sshOAuthPrompt(url, forward, redirectHost string, port int) string {
	return fmt.Sprintf(
		"You appear to be connected over SSH.\n"+
			"The OAuth callback needs to reach this machine's localhost.\n"+
			"Set up port forwarding from your LOCAL machine first:\n\n"+
			"  %s\n\n"+
			"Then open this URL in your LOCAL browser:\n  %s\n\n"+
			"If you cannot set up port forwarding, complete the login in your\n"+
			"browser. When the browser fails to connect (redirect to\n"+
			"%s:%d), copy the full URL from the address bar and paste it here.",
		forward, url, redirectHost, port)
}

// StatusLine returns a one-line summary of a stored credential for display
// in `nib login --list`. Example: "user@example.com (OAuth, expires in 3d)".
func (c Credential) StatusLine() string {
	switch c.Kind {
	case CredentialOAuth:
		label := c.Email
		if label == "" {
			label = c.AccountID
		}
		if label == "" {
			label = "OAuth account"
		}
		if c.ExpiresAt.IsZero() {
			return fmt.Sprintf("%s (OAuth)", label)
		}
		remaining := time.Until(c.ExpiresAt)
		if remaining < 0 {
			return fmt.Sprintf("%s (OAuth, expired)", label)
		}
		return fmt.Sprintf("%s (OAuth, expires in %s)", label, roundDuration(remaining))
	case CredentialAPIKey:
		return c.DisplayLabel()
	default:
		return "unknown"
	}
}

func roundDuration(d time.Duration) string {
	if d > 24*time.Hour {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if d > time.Hour {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d > time.Minute {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}
