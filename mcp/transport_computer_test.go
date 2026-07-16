package mcp

import (
	"context"
	"testing"

	"github.com/mudler/nib/types"
)

func TestStartTransportsGatesComputer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	off, err := StartTransports(ctx, types.Config{WorkingDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	on, err := StartTransports(ctx, types.Config{WorkingDir: t.TempDir(), Computer: types.ComputerConfig{Enabled: true, Command: "/bin/true"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(on) != len(off)+1 {
		t.Fatalf("enabled config should add exactly one transport: off=%d on=%d", len(off), len(on))
	}
}

func TestStartTransportsGatesBrowser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	off, err := StartTransports(ctx, types.Config{WorkingDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	on, err := StartTransports(ctx, types.Config{WorkingDir: t.TempDir(), Browser: types.BrowserConfig{Enabled: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(on) != len(off)+1 {
		t.Fatalf("enabled browser config should add exactly one transport: off=%d on=%d", len(off), len(on))
	}
}
