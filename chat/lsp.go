package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/lsp"
)

type lspArgs struct {
	Action string `json:"action" jsonschema:"one of: definition, references, symbols, hover, diagnostics, rename, code_actions, status"`
	File   string `json:"file,omitempty" jsonschema:"path to the source file (relative to the workspace or absolute). Required for all actions except status."`
	Line   int    `json:"line,omitempty" jsonschema:"1-indexed line number. Required for definition, references, hover, rename, and code_actions."`
	Symbol string `json:"symbol,omitempty" jsonschema:"substring of the symbol on that line; used to resolve the column. Required for definition, references, hover, rename, and code_actions."`
	NewName string `json:"new_name,omitempty" jsonschema:"new name for the symbol. Required for rename."`
}

type lspTool struct {
	manager     *lsp.Manager
	resolvePath func(string) string
}

func (t *lspTool) Run(args map[string]any) (string, any, error) {
	action, _ := args["action"].(string)
	if action == "" {
		return "lsp error: 'action' is required (definition, references, symbols, hover, diagnostics, rename, code_actions, status)", nil, nil
	}

	// status doesn't need a file — handle it before the file check.
	if action == "status" {
		return t.manager.Status(), nil, nil
	}

	file, _ := args["file"].(string)
	if file == "" {
		return "lsp error: 'file' is required", nil, nil
	}

	resolved := t.resolvePath(file)
	if _, err := os.Stat(resolved); err != nil {
		return "lsp error: file not found: " + resolved, nil, nil
	}

	client, err := t.manager.ServerForFile(resolved)
	if err != nil {
		return "lsp: " + err.Error(), nil, nil
	}

	ctx := context.Background()

	// For position-based actions, resolve the symbol to a column.
	line := 0
	col := 0
	if v, ok := args["line"].(float64); ok {
		line = int(v)
	}
	symbol, _ := args["symbol"].(string)

	switch action {
	case "definition":
		if line == 0 || symbol == "" {
			return "lsp error: definition requires 'file', 'line', and 'symbol'", nil, nil
		}
		col, err = resolveColumn(resolved, line, symbol)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		locs, err := client.Definition(ctx, resolved, line, col)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		return formatLocations("definition", locs), nil, nil

	case "references":
		if line == 0 || symbol == "" {
			return "lsp error: references requires 'file', 'line', and 'symbol'", nil, nil
		}
		col, err = resolveColumn(resolved, line, symbol)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		locs, err := client.References(ctx, resolved, line, col)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		return formatLocations("references", locs), nil, nil

	case "symbols":
		syms, err := client.DocumentSymbols(ctx, resolved)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		return formatSymbols(syms), nil, nil

	case "hover":
		if line == 0 || symbol == "" {
			return "lsp error: hover requires 'file', 'line', and 'symbol'", nil, nil
		}
		col, err = resolveColumn(resolved, line, symbol)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		hover, err := client.Hover(ctx, resolved, line, col)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		return formatHover(hover), nil, nil

	case "diagnostics":
		diags, err := client.Diagnostics(ctx, resolved)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		return formatDiagnostics(diags), nil, nil

	case "rename":
		if line == 0 || symbol == "" {
			return "lsp error: rename requires 'file', 'line', and 'symbol'", nil, nil
		}
		newName, _ := args["new_name"].(string)
		if newName == "" {
			return "lsp error: rename requires 'new_name'", nil, nil
		}
		col, err = resolveColumn(resolved, line, symbol)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		edit, err := client.Rename(ctx, resolved, line, col, newName)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		return formatWorkspaceEdit(edit), nil, nil

	case "code_actions":
		if line == 0 || symbol == "" {
			return "lsp error: code_actions requires 'file', 'line', and 'symbol'", nil, nil
		}
		col, err = resolveColumn(resolved, line, symbol)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		actions, err := client.CodeActions(ctx, resolved, line, col)
		if err != nil {
			return "lsp error: " + err.Error(), nil, nil
		}
		return formatCodeActions(actions), nil, nil

	default:
		return "lsp error: unknown action '" + action + "' (use definition, references, symbols, hover, diagnostics, rename, code_actions, or status)", nil, nil
	}
}

// resolveColumn finds the 0-indexed column of the first occurrence of
// symbol on the given 1-indexed line. This is how omp resolves positions:
// the LLM passes a substring, and we find it in the file.
func resolveColumn(path string, line int, symbol string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if line < 1 || line > len(lines) {
		return 0, fmt.Errorf("line %d out of range (file has %d lines)", line, len(lines))
	}
	src := lines[line-1]
	col := strings.Index(src, symbol)
	if col < 0 {
		return 0, fmt.Errorf("symbol %q not found on line %d", symbol, line)
	}
	return col, nil
}

func formatLocations(label string, locs []lsp.Location) string {
	if len(locs) == 0 {
		return "no " + label + " found"
	}
	var b strings.Builder
	for i, loc := range locs {
		path := uriToPath(loc.URI)
		start := loc.Range.Start
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s:%d:%d", path, start.Line+1, start.Character+1)
	}
	return b.String()
}

func formatSymbols(syms []lsp.Symbol) string {
	if len(syms) == 0 {
		return "no symbols found"
	}
	var b strings.Builder
	for _, sym := range syms {
		formatSymbol(&b, &sym, 0)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatSymbol(b *strings.Builder, sym *lsp.Symbol, indent int) {
	for i := 0; i < indent; i++ {
		b.WriteString("  ")
	}
	kindStr := symbolKindString(sym.Kind)
	line := sym.Range.Start.Line + 1
	if kindStr != "" {
		fmt.Fprintf(b, "%s: %s (line %d)\n", kindStr, sym.Name, line)
	} else {
		fmt.Fprintf(b, "%s (line %d)\n", sym.Name, line)
	}
	for _, child := range sym.Children {
		formatSymbol(b, &child, indent+1)
	}
}

func symbolKindString(kind int) string {
	switch kind {
	case lsp.SymbolKindClass, lsp.SymbolKindInterface, lsp.SymbolKindStruct:
		return "Type"
	case lsp.SymbolKindFunction:
		return "Function"
	case lsp.SymbolKindMethod:
		return "Method"
	case lsp.SymbolKindVariable, lsp.SymbolKindField, lsp.SymbolKindProperty:
		return "Variable"
	case lsp.SymbolKindConstant:
		return "Constant"
	case lsp.SymbolKindEnum, lsp.SymbolKindEnumMember:
		return "Enum"
	case lsp.SymbolKindModule, lsp.SymbolKindNamespace, lsp.SymbolKindPackage:
		return "Module"
	case lsp.SymbolKindConstructor:
		return "Constructor"
	default:
		return "Symbol"
	}
}

func uriToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

func formatHover(hover *lsp.Hover) string {
	if hover == nil {
		return "no hover information"
	}
	return hover.Contents.Value
}

func formatDiagnostics(diags []lsp.Diagnostic) string {
	if len(diags) == 0 {
		return "no diagnostics"
	}
	var b strings.Builder
	for _, d := range diags {
		severity := "info"
		switch d.Severity {
		case lsp.SeverityError:
			severity = "error"
		case lsp.SeverityWarning:
			severity = "warning"
		case lsp.SeverityInformation:
			severity = "info"
		case lsp.SeverityHint:
			severity = "hint"
		}
		start := d.Range.Start
		b.WriteString(fmt.Sprintf("%d:%d %s: %s", start.Line+1, start.Character+1, severity, d.Message))
		if d.Source != "" {
			b.WriteString(" (" + d.Source + ")")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatWorkspaceEdit(edit *lsp.WorkspaceEdit) string {
	if edit == nil || len(edit.Changes) == 0 {
		return "no changes"
	}
	var b strings.Builder
	for uri, edits := range edit.Changes {
		path := uriToPath(uri)
		fmt.Fprintf(&b, "%s:\n", path)
		for _, te := range edits {
			s := te.Range.Start
			e := te.Range.End
			fmt.Fprintf(&b, "  %d:%d-%d:%d: %q\n", s.Line+1, s.Character+1, e.Line+1, e.Character+1, te.NewText)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatCodeActions(actions []lsp.CodeAction) string {
	if len(actions) == 0 {
		return "no code actions available"
	}
	var b strings.Builder
	for i, a := range actions {
		if i > 0 {
			b.WriteString("\n")
		}
		if a.Kind != "" {
			fmt.Fprintf(&b, "[%s] %s", a.Kind, a.Title)
		} else {
			b.WriteString(a.Title)
		}
	}
	return b.String()
}

func lspToolDefinition(mgr *lsp.Manager, resolvePath func(string) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&lspTool{manager: mgr, resolvePath: resolvePath}, lspArgs{},
		"lsp",
		"Symbol-aware code navigation using a Language Server. Actions:\n\n"+
			"definition — go-to-definition: pass file, line (1-indexed), and symbol (a substring on that line) "+
			"to find where the symbol is declared.\n"+
			"references — find all callsites: same parameters as definition, returns every file:line that references the symbol.\n"+
			"symbols — document outline: pass file only, returns all top-level symbols with their kinds and line numbers.\n"+
			"hover — type/signature info: pass file, line, and symbol to get the hover (type, doc) at that position.\n"+
			"diagnostics — pass file to get compiler/linter diagnostics (errors, warnings) for that file.\n"+
			"rename — pass file, line, symbol, and new_name to get a preview of all edits a rename would make.\n"+
			"code_actions — pass file, line, and symbol to get available refactoring/fix-it actions at that position.\n"+
			"status — no parameters; shows all configured language servers and whether they are running.\n\n"+
			"When a language server is available, prefer 'lsp references' over grep for finding callsites — "+
			"it follows imports, interface methods, and re-exports that text search misses. "+
			"Prefer 'lsp hover' over guessing types. The tool is present when at least one server is configured or auto-detected.",
	)
}

// resolveWorkspacePathForLSP resolves a path relative to the working
// directory, the same way other tools do.
func resolveWorkspacePathForLSP(workingDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workingDir, p)
}
