package cmd

import "testing"

func TestParseMCPFlagsDefaultsToStdio(t *testing.T) {
	opts, err := parseMCPFlags(nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.HTTP {
		t.Fatal("default must be stdio (HTTP=false)")
	}
}

func TestParseMCPFlagsHTTP(t *testing.T) {
	opts, err := parseMCPFlags([]string{"--http", "--addr", "127.0.0.1:9000"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !opts.HTTP || opts.Addr != "127.0.0.1:9000" {
		t.Fatalf("opts = %+v, want HTTP + addr 127.0.0.1:9000", opts)
	}
}
