package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mudler/nib/llmprovider/registry"
)

// Wire fields follow openai/codex 16845bb8e85d781f6e745bdec011384450929ff9,
// app-server-protocol/schema/typescript/v2/{Model,ModelListParams,ModelListResponse}.ts.
func TestModelServerProcess(t *testing.T) {
	if os.Getenv("NIB_MODEL_HELPER") != "1" {
		return
	}
	_ = os.WriteFile(os.Getenv("NIB_MODEL_PID"), []byte(strconv.Itoa(os.Getpid())), 0600)
	mode := os.Getenv("NIB_MODEL_MODE")
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	initialized := false
	pages := 0
	for scanner.Scan() {
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		switch req.Method {
		case "initialize":
			if mode == "hang" {
				time.Sleep(time.Hour)
			}
			enc.Encode(map[string]any{"id": req.ID, "result": map[string]any{}})
		case "initialized":
			initialized = true
		case "model/list":
			if !initialized || req.Params["includeHidden"] != false || req.Params["limit"] != float64(100) {
				os.Exit(3)
			}
			pages++
			if mode == "unsupported" || (mode == "later" && pages == 2) {
				enc.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32601, "message": "unsupported"}})
				continue
			}
			if mode == "malformed" {
				fmt.Println("not json")
				continue
			}
			if mode == "eof" {
				fmt.Fprintln(os.Stderr, "fixture failure")
				os.Exit(4)
			}
			data := []map[string]any{{"id": "future-exact", "model": "future-exact", "hidden": false}, {"id": "secret", "model": "secret", "hidden": true}, {"id": "future-exact", "model": "future-exact", "hidden": false}}
			var next any
			if pages == 1 {
				next = "page2"
			} else {
				data = []map[string]any{{"id": "second-exact", "model": "second-exact", "hidden": false}}
			}
			if mode == "cycle" {
				next = "page2"
			}
			if mode == "cap" {
				next = fmt.Sprint(pages)
			}
			if mode == "empty" {
				data = nil
				next = nil
			}
			result := map[string]any{"data": data, "nextCursor": next}
			if mode == "missing" {
				delete(result, "nextCursor")
			}
			enc.Encode(map[string]any{"id": req.ID, "result": result})
		default:
			os.Exit(5) // Discovery must never start a thread or turn.
		}
	}
	os.Exit(0)
}

func TestDiscoverModelsProcess(t *testing.T) {
	for _, mode := range []string{"pages", "empty", "cycle", "cap", "missing", "unsupported", "later", "malformed", "eof", "hang"} {
		t.Run(mode, func(t *testing.T) {
			pidFile := t.TempDir() + "/pid"
			t.Setenv("NIB_MODEL_HELPER", "1")
			t.Setenv("NIB_MODEL_PID", pidFile)
			t.Setenv("NIB_MODEL_MODE", mode)
			l := New(Config{Command: os.Args[0], Args: []string{"-test.run=^TestModelServerProcess$"}})
			d, ok := any(l).(registry.ModelDiscoverer)
			if !ok {
				t.Fatal("app-server has no result-bearing model discovery")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r := d.DiscoverModels(ctx)
			if mode == "pages" {
				if r.Err != nil || r.Partial || strings.Join(r.IDs, ",") != "future-exact,second-exact" {
					t.Fatalf("result=%+v", r)
				}
			} else if mode == "empty" {
				if r.Err != nil || r.Partial || len(r.IDs) != 0 {
					t.Fatalf("result=%+v", r)
				}
			} else {
				if r.Err == nil || !r.Partial {
					t.Fatalf("want incomplete diagnostic: %+v", r)
				}
				if mode == "later" && strings.Join(r.IDs, ",") != "future-exact" {
					t.Fatalf("lost IDs: %+v", r)
				}
			}
			pidBytes, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, _ := strconv.Atoi(string(pidBytes))
			if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
				t.Fatalf("child %d not reaped: %v", pid, err)
			}
		})
	}
}
