package cmd

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/nib/auth"
)

func loginIO(t *testing.T, input string, run func() int) (int, string) {
	t.Helper()
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if _, err := in.WriteString(input); err != nil {
		t.Fatal(err)
	}
	in.Seek(0, 0)
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = in, out, out
	defer func() { os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr }()
	code := run()
	out.Seek(0, 0)
	data, _ := io.ReadAll(out)
	return code, string(data)
}

func TestAnthropicAPIKeyCLIAndLogoutCoexistence(t *testing.T) {
	root := t.TempDir()
	store := auth.NewStore(filepath.Join(root, "credentials.json"))
	oauth := auth.Credential{ProviderID: "anthropic", Kind: auth.CredentialOAuth, AccessToken: "oauth-access-sentinel", RefreshToken: "oauth-refresh-sentinel"}
	if err := store.Save(oauth); err != nil {
		t.Fatal(err)
	}
	code, out := loginIO(t, "", func() int { return RunLoginCommand("nib", root, nil) })
	if code != 1 || !strings.Contains(out, "anthropic-api-key") || !strings.Contains(out, "anthropic ") {
		t.Fatalf("list: %d %s", code, out)
	}
	oauth, _, _ = store.Get("anthropic")
	key := "cli-private-key-sentinel"
	code, out = loginIO(t, key+"\n", func() int { return RunLoginCommand("nib", root, []string{"anthropic-api-key"}) })
	if code != 0 || strings.Contains(out, key) || strings.Contains(out, "https://") || strings.Contains(out, "OAuth") {
		t.Fatalf("key login: %d %s", code, out)
	}
	saved, ok, err := store.Get("anthropic-api-key")
	if err != nil || !ok || saved.Kind != auth.CredentialAPIKey || saved.APIKey != key {
		t.Fatalf("key credential: %+v %v", saved, err)
	}
	code, out = loginIO(t, "", func() int { return RunLoginCommand("nib", root, []string{"--list"}) })
	if code != 0 || !strings.Contains(out, "anthropic-api-key") || !strings.Contains(out, "anthropic ") {
		t.Fatalf("saved login list: %d %s", code, out)
	}
	got, _, _ := store.Get("anthropic")
	if !reflect.DeepEqual(got, oauth) {
		t.Fatal("key login modified OAuth")
	}
	code, _ = loginIO(t, "", func() int { return RunLogoutCommand("nib", root, []string{"anthropic"}) })
	if code != 0 {
		t.Fatal("OAuth logout")
	}
	got, _, _ = store.Get("anthropic-api-key")
	if !reflect.DeepEqual(got, saved) {
		t.Fatal("OAuth logout changed key")
	}
	if err := store.Save(oauth); err != nil {
		t.Fatal(err)
	}
	code, _ = loginIO(t, "", func() int { return RunLogoutCommand("nib", root, []string{"anthropic-api-key"}) })
	if code != 0 {
		t.Fatal("key logout")
	}
	got, _, _ = store.Get("anthropic")
	if !reflect.DeepEqual(got, oauth) {
		t.Fatal("key logout modified OAuth")
	}
}
