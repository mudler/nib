package chat

import (
	"strings"
	"testing"
)

func TestLSPToolDefinition(t *testing.T) {
	td := lspToolDefinition(nil, func(p string) string { return p }).Tool()
	if td.Function.Name != "lsp" {
		t.Fatalf("name = %q, want \"lsp\"", td.Function.Name)
	}
}

func TestLSPToolDefinitionDescription(t *testing.T) {
	td := lspToolDefinition(nil, func(p string) string { return p }).Tool()
	desc := td.Function.Description
	if desc == "" {
		t.Fatal("description is empty")
	}
	for _, want := range []string{"definition", "references", "symbols", "hover", "diagnostics", "rename", "code_actions", "status"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description should mention %q", want)
		}
	}
}

func TestSymbolKindString(t *testing.T) {
	tests := []struct {
		kind int
		want string
	}{
		{12, "Function"},  // SymbolKindFunction
		{6, "Method"},      // SymbolKindMethod
		{5, "Type"},        // SymbolKindClass
		{23, "Type"},       // SymbolKindStruct
		{11, "Type"},       // SymbolKindInterface
		{13, "Variable"},   // SymbolKindVariable
		{14, "Constant"},   // SymbolKindConstant
		{0, "Symbol"},      // Unknown
	}
	for _, tc := range tests {
		got := symbolKindString(tc.kind)
		if got != tc.want {
			t.Errorf("symbolKindString(%d) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestURItoPath(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"file:///home/user/project/main.go", "/home/user/project/main.go"},
		{"/home/user/project/main.go", "/home/user/project/main.go"},
	}
	for _, tc := range tests {
		got := uriToPath(tc.uri)
		if got != tc.want {
			t.Errorf("uriToPath(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

func TestFormatHoverNil(t *testing.T) {
	got := formatHover(nil)
	if got != "no hover information" {
		t.Errorf("formatHover(nil) = %q, want \"no hover information\"", got)
	}
}

func TestFormatDiagnosticsEmpty(t *testing.T) {
	got := formatDiagnostics(nil)
	if got != "no diagnostics" {
		t.Errorf("formatDiagnostics(nil) = %q, want \"no diagnostics\"", got)
	}
}

func TestFormatWorkspaceEditNil(t *testing.T) {
	got := formatWorkspaceEdit(nil)
	if got != "no changes" {
		t.Errorf("formatWorkspaceEdit(nil) = %q, want \"no changes\"", got)
	}
}

func TestFormatCodeActionsEmpty(t *testing.T) {
	got := formatCodeActions(nil)
	if got != "no code actions available" {
		t.Errorf("formatCodeActions(nil) = %q, want \"no code actions available\"", got)
	}
}
