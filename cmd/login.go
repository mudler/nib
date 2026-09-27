package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider/copilot"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/provider"
)

// RunLoginCommand handles `nib login [provider]` and `nib login --list`.
// Without a provider, it lists loginable providers. With a provider, it runs
// the appropriate flow (OAuth or API key).
func RunLoginCommand(programName, baseDir string, args []string) int {
	prog := runnableName(programName)
	root := plugin.BaseDirIn(baseDir)
	store := auth.NewStore(credentialPath(root))

	if len(args) == 0 {
		loginUsage(prog)
		return 1
	}

	if args[0] == "--list" || args[0] == "-l" {
		return loginList(prog, store)
	}

	// --device flag forces the device-code flow for providers that support it
	// (even if their default LoginKind is LoginOAuthCode). This is the
	// SSH-friendly path: no localhost callback server needed.
	useDevice := false
	providerID := args[0]
	rest := args[1:]
	if len(rest) > 0 && (rest[0] == "--device" || rest[0] == "-d") {
		useDevice = true
		rest = rest[1:]
	}
	_ = rest // currently no positional args after provider ID

	def, ok := provider.Get(providerID)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s login: unknown provider %q\n", prog, providerID)
		fmt.Fprintf(os.Stderr, "Available: %s\n", providerIDs(provider.Loginable()))
		return 1
	}

	if def.LoginKind == provider.LoginNone {
		fmt.Fprintf(os.Stderr, "%s login: %s has no login flow (use env var %s)\n", prog, def.ID, def.EnvVar)
		return 1
	}

	ctx := context.Background()

	// --device flag: switch to device-code flow if the provider has a DeviceURL.
	if useDevice {
		if def.DeviceURL == "" {
			fmt.Fprintf(os.Stderr, "%s login: %s does not support device-code flow (no device authorization endpoint)\n", prog, def.ID)
			fmt.Fprintf(os.Stderr, "Use the standard OAuth flow instead. Over SSH, set up port forwarding:\n")
			fmt.Fprintf(os.Stderr, "  ssh -L %d:localhost:%d <user>@<this-host>\n", def.CallbackPort, def.CallbackPort)
			return 1
		}
		// Temporarily switch the login kind to device-code.
		def.LoginKind = provider.LoginDeviceCode
	}

	switch def.LoginKind {
	case provider.LoginOAuthCode, provider.LoginDeviceCode:
		return loginFlow(ctx, prog, store, def)
	case provider.LoginAPIKey:
		return loginAPIKey(prog, store, def)
	case provider.LoginCopilot:
		return loginCopilotToken(prog, store, def)
	default:
		fmt.Fprintf(os.Stderr, "%s login: %s has an unsupported login kind\n", prog, def.ID)
		return 1
	}
}

// RunLogoutCommand handles `nib logout [provider]`.
func RunLogoutCommand(programName, baseDir string, args []string) int {
	prog := runnableName(programName)
	root := plugin.BaseDirIn(baseDir)
	store := auth.NewStore(credentialPath(root))

	if len(args) == 0 {
		return logoutList(prog, store)
	}

	def, ok := provider.Get(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "%s logout: unknown provider %q\n", prog, args[0])
		return 1
	}

	cred, ok, err := store.Get(def.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s logout: %v\n", prog, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "%s logout: not logged in to %s\n", prog, def.ID)
		return 1
	}
	_ = cred

	if err := store.Delete(def.ID); err != nil {
		fmt.Fprintf(os.Stderr, "%s logout: %v\n", prog, err)
		return 1
	}
	fmt.Printf("Logged out of %s (%s)\n", def.Name, def.ID)
	return 0
}

// loginFlow handles both OAuth-code and device-code login by delegating to
// auth.StartLogin, which detects SSH sessions, auto-switches to device flow
// when appropriate, and builds the port-forward hint / paste-code prompt.
func loginFlow(ctx context.Context, prog string, store *auth.Store, def provider.Definition) int {
	flow, err := auth.StartLogin(ctx, store, def)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s login: %v\n", prog, err)
		return 1
	}

	fmt.Printf("Starting %s login for %s...\n", def.LoginKind, def.Name)
	fmt.Println(flow.Prompt)

	if flow.URL != "" && !flow.IsSSH {
		openBrowser(flow.URL)
	}

	if flow.AcceptsPastedURL() {
		return loginOAuthWithPaste(ctx, prog, def, flow)
	}

	fmt.Println("Waiting for authorization...")
	cred, err := flow.Complete(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s login: %v\n", prog, err)
		return 1
	}
	fmt.Printf("Logged in to %s as %s\n", def.Name, cred.DisplayLabel())
	return 0
}

// loginOAuthWithPaste races the callback server (flow.Complete) against
// stdin, feeding pasted URLs into flow.ManualCallback: the fallback for a
// browser whose redirect cannot reach this machine, over SSH or not.
func loginOAuthWithPaste(ctx context.Context, prog string, def provider.Definition, flow *auth.LoginFlow) int {
	fmt.Println("Waiting for authorization (callback or pasted URL)...")

	type result struct {
		cred auth.Credential
		err  error
	}
	completeCh := make(chan result, 1)
	go func() {
		cred, err := flow.Complete(ctx)
		completeCh <- result{cred, err}
	}()

	stdinCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			stdinCh <- strings.TrimSpace(scanner.Text())
		}
	}()

	for {
		select {
		case res := <-completeCh:
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "%s login: %v\n", prog, res.err)
				return 1
			}
			fmt.Printf("Logged in to %s as %s\n", def.Name, res.cred.DisplayLabel())
			return 0
		case raw := <-stdinCh:
			if raw == "" {
				continue
			}
			if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
				fmt.Println("That doesn't look like a URL. Paste the full callback URL from your browser's address bar.")
				continue
			}
			if err := flow.ManualCallback(raw); err != nil {
				fmt.Fprintf(os.Stderr, "%s login: invalid pasted URL: %v\n", prog, err)
				continue
			}
			res := <-completeCh
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "%s login: %v\n", prog, res.err)
				return 1
			}
			fmt.Printf("Logged in to %s as %s\n", def.Name, res.cred.DisplayLabel())
			return 0
		}
	}
}

func loginCopilotToken(prog string, store *auth.Store, def provider.Definition) int {
	fmt.Printf("Importing GitHub Copilot token for %s...\n", def.Name)
	token, err := copilot.ResolveToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s login: %v\n", prog, err)
		fmt.Fprintf(os.Stderr, "Install gh CLI and run 'gh auth login', or set %s\n", def.EnvVar)
		return 1
	}
	cred, err := auth.LoginAPIKey(store, def, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s login: %v\n", prog, err)
		return 1
	}
	fmt.Printf("Logged in to %s (%s)\n", def.Name, cred.StatusLine())
	return 0
}

func loginAPIKey(prog string, store *auth.Store, def provider.Definition) int {
	key, err := readSecret(fmt.Sprintf("Enter API key for %s: ", def.Name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n%s login: read key: %v\n", prog, err)
		return 1
	}
	if key == "" {
		fmt.Fprintf(os.Stderr, "%s login: empty key, aborting\n", prog)
		return 1
	}
	baseURL := ""
	if def.NeedsBaseURL() {
		fmt.Printf("Base URL for %s: ", def.Name)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintf(os.Stderr, "\n%s login: read base URL: %v\n", prog, err)
			return 1
		}
		baseURL = strings.TrimSpace(line)
	}
	cred, err := auth.LoginAPIKeyAt(store, def, key, baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s login: %v\n", prog, err)
		return 1
	}
	fmt.Printf("Logged in to %s (%s)\n", def.Name, cred.DisplayLabel())
	return 0
}

// readSecret prompts for a line without echoing it when stdin is a terminal,
// and reads it plainly when piped (`echo $KEY | nib login groq`).
func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func loginList(prog string, store *auth.Store) int {
	creds, err := store.All()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s login --list: %v\n", prog, err)
		return 1
	}
	if len(creds) == 0 {
		fmt.Println("No providers logged in. Run: nib login <provider>")
		return 0
	}
	for _, c := range creds {
		fmt.Printf("%s  %s\n", c.ProviderID, c.StatusLine())
	}
	return 0
}

func logoutList(prog string, store *auth.Store) int {
	creds, err := store.All()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s logout: %v\n", prog, err)
		return 1
	}
	if len(creds) == 0 {
		fmt.Println("No providers logged in.")
		return 0
	}
	fmt.Println("Logged in to:")
	for _, c := range creds {
		fmt.Printf("  %s  %s\n", c.ProviderID, c.StatusLine())
	}
	fmt.Printf("\nRun: %s logout <provider>\n", prog)
	return 0
}

func loginUsage(prog string) {
	fmt.Printf("Usage: %s login <provider> [--device]\n", prog)
	fmt.Printf("       %s login --list\n\n", prog)
	fmt.Println("Providers with login:")
	for _, d := range provider.Loginable() {
		extras := []string{string(d.LoginKind)}
		if d.DeviceURL != "" {
			extras = append(extras, "device-code supported")
		}
		fmt.Printf("  %-12s  %s  (%s)\n", d.ID, d.Name, strings.Join(extras, ", "))
	}
	fmt.Println("\n--device  Use the device-code flow (RFC 8628) when available.")
	fmt.Println("         This avoids the localhost callback and works over SSH without port forwarding.")
}

func credentialPath(root string) string {
	return root + string(os.PathSeparator) + "credentials.json"
}

func providerIDs(defs []provider.Definition) string {
	ids := make([]string, len(defs))
	for i, d := range defs {
		ids[i] = d.ID
	}
	return strings.Join(ids, ", ")
}

// openBrowser attempts to open url in the user's default browser. Failure is
// non-fatal — the URL is already printed for manual entry.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}
