package chat

import (
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tool list handed to the model is built in allClients order, and it is
// serialized into the prompt ahead of the user's turn. Any reordering therefore
// moves the prompt's token prefix and throws away the server's KV cache for it,
// which on CPU costs a full reprocess of the whole system+tools block (seconds).
// cfgClients is a map, so ranging it directly made that order random per turn.

// TestAllClientsOrderIsStable is the regression proper: the same session must
// produce byte-identical client order on every call.
func TestAllClientsOrderIsStable(t *testing.T) {
	s := newReloadTestSession(t)
	// Enough entries that Go's map seed would shuffle them: with 8 keys the
	// odds of 50 consecutive identical random orders are ~(1/8!)^49.
	for _, name := range []string{"thunderbird", "knowledge", "notary", "calendar", "mail", "files", "shell", "weather"} {
		s.cfgClients[name] = &sdkmcp.ClientSession{}
	}

	first := s.allClients()
	for i := 0; i < 50; i++ {
		got := s.allClients()
		if len(got) != len(first) {
			t.Fatalf("call %d: length changed: got %d, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("call %d: client order changed at index %d — the prompt prefix is not stable", i, j)
			}
		}
	}
}

// TestAllClientsOrderIsSortedByName pins *which* order, so the prefix also
// survives a process restart (a map-seed-stable order would not).
func TestAllClientsOrderIsSortedByName(t *testing.T) {
	s := newReloadTestSession(t)
	byName := map[string]*sdkmcp.ClientSession{}
	for _, name := range []string{"zeta", "alpha", "mike", "bravo"} {
		sess := &sdkmcp.ClientSession{}
		s.cfgClients[name] = sess
		byName[name] = sess
	}

	got := s.allClients()
	want := []*sdkmcp.ClientSession{byName["alpha"], byName["bravo"], byName["mike"], byName["zeta"]}
	if len(got) != len(want) {
		t.Fatalf("got %d clients, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: config clients are not in sorted-name order", i)
		}
	}
}

// TestAllClientsKeepsBuiltinsAheadOfConfigServers pins the grouping: built-ins
// and skills are a fixed slice, so they must stay at the head of the prefix and
// never interleave with the sorted config servers.
func TestAllClientsKeepsBuiltinsAheadOfConfigServers(t *testing.T) {
	s := newReloadTestSession(t)
	builtinA, builtinB := &sdkmcp.ClientSession{}, &sdkmcp.ClientSession{}
	skills := &sdkmcp.ClientSession{}
	cfg := &sdkmcp.ClientSession{}
	s.clients = []*sdkmcp.ClientSession{builtinA, builtinB}
	s.skillsClient = skills
	s.cfgClients["alpha"] = cfg

	got := s.allClients()
	want := []*sdkmcp.ClientSession{builtinA, builtinB, skills, cfg}
	if len(got) != len(want) {
		t.Fatalf("got %d clients, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: built-ins/skills must precede config servers", i)
		}
	}
}
