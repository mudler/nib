package bedrock

import (
	"context"
	"fmt"
	"github.com/mudler/nib/llmprovider/registry"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverySourcesAndCompleteness(t *testing.T) {
	for _, mode := range []string{"full", "profiles", "foundation", "both", "later", "cycle", "cap", "empty", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/foundation-models" {
					if mode == "foundation" || mode == "both" {
						http.Error(w, "foundation failure", 500)
						return
					}
					if mode == "empty" {
						fmt.Fprint(w, `{}`)
						return
					}
					fmt.Fprint(w, `{"modelSummaries":[{"modelId":"foundation"}]}`)
					return
				}
				calls++
				if mode == "profiles" || mode == "both" || (mode == "later" && calls > 1) {
					http.Error(w, "profile failure", 500)
					return
				}
				if mode == "empty" {
					fmt.Fprint(w, `{}`)
					return
				}
				next := ""
				if mode == "cycle" || mode == "later" {
					next = "next"
				}
				if mode == "cap" {
					next = fmt.Sprint(calls)
				}
				fmt.Fprintf(w, `{"inferenceProfileSummaries":[{"inferenceProfileId":"profile"},{"inferenceProfileId":"profile"}],"nextToken":%q}`, next)
			}))
			defer srv.Close()
			old := controlPlaneURL
			controlPlaneURL = func(string) string { return srv.URL }
			defer func() { controlPlaneURL = old }()
			l := New(Config{BearerToken: "test"})
			d, ok := any(l).(registry.ModelDiscoverer)
			if !ok {
				t.Fatal("missing per-call discovery")
			}
			ctx := context.Background()
			if mode == "cancel" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			r := d.DiscoverModels(ctx)
			partial := mode != "full" && mode != "empty"
			want := 2
			if mode == "profiles" || mode == "foundation" {
				want = 1
			}
			if mode == "both" || mode == "empty" || mode == "cancel" {
				want = 0
			}
			if r.Partial != partial || (r.Err != nil) != partial || len(r.IDs) != want {
				t.Fatalf("result=%+v want partial=%v ids=%d", r, partial, want)
			}
			// Completeness belongs to this call, not the preceding failed call.
			mode = "full"
			r = d.DiscoverModels(context.Background())
			if r.Partial || r.Err != nil || len(r.IDs) != 2 {
				t.Fatalf("second call=%+v", r)
			}
		})
	}
}
