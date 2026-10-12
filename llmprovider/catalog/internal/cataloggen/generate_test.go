package cataloggen

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/klauspost/compress/zstd"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const revision = "c8913466dd7bf92040de911f8e00e30b8f4e48a2"
const valid = `{"p":{"M":{"id":"M","provider":"p","name":"Model","api":"openai-completions","baseUrl":"https://example.com","contextWindow":100,"maxTokens":20,"compat":{"maxTokensField":"max_tokens"},"future":true}}}`

func TestRejectAndPreserve(t *testing.T) {
	cases := []string{"", "<html>error</html>", "{", "{}", "[]", "null", `{"p":{}}`, `{"p":null}`, strings.Replace(valid, `"id":"M"`, `"id":"wrong"`, 1), strings.Replace(valid, `"provider":"p"`, `"provider":"wrong"`, 1)}
	for _, v := range []string{`-1`, `0`, `1.5`, `"100"`, `true`, `9007199254740993`} {
		cases = append(cases, strings.Replace(valid, `:100`, `:`+v, 1))
	}
	cases = append(cases, strings.Replace(valid, `"max_tokens"`, `true`, 1), strings.Replace(valid, `"compat":{`, `"compat":{"alwaysSendMaxTokens":"yes",`, 1))
	cases = append(cases, strings.Replace(valid, `"compat":{`, `"compat":{"supportsReasoningSummary":"yes",`, 1))
	for i, body := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			dir := t.TempDir()
			for _, n := range []string{"catalog.json.zst", "provenance.json"} {
				os.WriteFile(filepath.Join(dir, n), []byte("old"), 0644)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer srv.Close()
			if _, err := Generate(srv.Client(), srv.URL, dir, revision, true); err == nil {
				t.Error("invalid source accepted")
			}
			for _, n := range []string{"catalog.json.zst", "provenance.json"} {
				b, _ := os.ReadFile(filepath.Join(dir, n))
				if string(b) != "old" {
					t.Errorf("%s overwritten", n)
				}
			}
		})
	}
}
func TestHTTPFailures(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(fmt.Sprint(slow), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if slow {
					time.Sleep(50 * time.Millisecond)
				} else {
					w.WriteHeader(503)
				}
				fmt.Fprint(w, valid)
			}))
			defer srv.Close()
			client := srv.Client()
			client.Timeout = 10 * time.Millisecond
			if _, err := Generate(client, srv.URL, t.TempDir(), revision, true); err == nil {
				t.Fatal("HTTP failure accepted")
			}
		})
	}
}
func TestPinnedDeterminismAndDelta(t *testing.T) {
	body := valid
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer srv.Close()
	dir := t.TempDir()
	run := func(refresh bool) string {
		t.Helper()
		s, e := Generate(srv.Client(), srv.URL, dir, revision, refresh)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	run(true)
	a, _ := os.ReadFile(filepath.Join(dir, "catalog.json.zst"))
	p, _ := os.ReadFile(filepath.Join(dir, "provenance.json"))
	var meta map[string]any
	if err := json.Unmarshal(p, &meta); err != nil {
		t.Fatalf("missing valid provenance: %v", err)
	}
	dec, _ := zstd.NewReader(nil)
	defer dec.Close()
	raw, e := dec.DecodeAll(a, nil)
	if e != nil || string(raw) != valid {
		t.Fatal("raw source not retained")
	}
	if meta["revision"] != revision || meta["sha256"] != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		t.Fatal("provenance mismatch")
	}
	run(false)
	b, _ := os.ReadFile(filepath.Join(dir, "catalog.json.zst"))
	q, _ := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if !bytes.Equal(a, b) || !bytes.Equal(p, q) {
		t.Fatal("not deterministic")
	}
	body = strings.Replace(valid, ":100", ":200", 1)
	if _, e := Generate(srv.Client(), srv.URL, dir, revision, false); e == nil {
		t.Fatal("reproduction accepted changed digest")
	}
	b, _ = os.ReadFile(filepath.Join(dir, "catalog.json.zst"))
	if !bytes.Equal(a, b) {
		t.Fatal("digest failure overwrote artifact")
	}
	s := run(true)
	if !strings.Contains(s, "contextWindow: 100 -> 200") {
		t.Fatalf("missing readable limit delta: %s", s)
	}
	body = `{"z":{"Z":{"id":"Z","provider":"z"}},"a":{"A":{"id":"A","provider":"a"}}}`
	s = run(true)
	want := "+ provider a\n+ provider z\n- provider p\n+ model a/A\n+ model z/Z\n- model p/M\n"
	if s != want {
		t.Fatalf("sorted delta got %q want %q", s, want)
	}
}

// A blocked provenance destination must not leave the artifact at a new revision.
func TestSecondReplaceFailureRollsBackArtifact(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			body := valid
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer srv.Close()
			dir := t.TempDir()
			artifact := filepath.Join(dir, "catalog.json.zst")
			provenance := filepath.Join(dir, "provenance.json")
			var before []byte
			if existing {
				if _, err := Generate(srv.Client(), srv.URL, dir, revision, true); err != nil {
					t.Fatal(err)
				}
				before, _ = os.ReadFile(artifact)
				if err := os.Remove(provenance); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(provenance, 0755); err != nil {
				t.Fatal(err)
			}
			body = strings.Replace(valid, ":100", ":200", 1)
			if _, err := Generate(srv.Client(), srv.URL, dir, revision, true); err == nil {
				t.Fatal("blocked replacement succeeded")
			}
			after, err := os.ReadFile(artifact)
			if existing && (err != nil || !bytes.Equal(before, after)) {
				t.Fatal("failed pair replacement changed old artifact")
			}
			if !existing && !os.IsNotExist(err) {
				t.Fatal("failed initial pair replacement left artifact behind")
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".") {
					t.Errorf("temporary file leaked: %s", entry.Name())
				}
			}
		})
	}
}

func TestDeltaConsumedReasoningEfforts(t *testing.T) {
	before, err := validate([]byte(strings.Replace(valid, `"max_tokens"`, `"max_tokens","supportedReasoningEfforts":["low"]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	after, err := validate([]byte(strings.Replace(valid, `"max_tokens"`, `"max_tokens","supportedReasoningEfforts":["high"]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if got := delta(before, after); !strings.Contains(got, `"low"`) || !strings.Contains(got, `"high"`) {
		t.Fatalf("missing consumed capability change: %s", got)
	}
}
