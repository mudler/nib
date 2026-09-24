package lsp

// Position is a 0-indexed line and character position in a document.
// LSP uses 0-indexed lines; the chat tool converts from 1-indexed.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a span between two positions.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a URI + range, the result of definition/references.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// InitializeParams is the LSP initialize request payload.
type InitializeParams struct {
	RootURI      string             `json:"rootUri,omitempty"`
	Capabilities ClientCapabilities `json:"capabilities"`
	ProcessID    int                `json:"processId"`
}

// ClientCapabilities declares which features the client supports.
type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument"`
}

// TextDocumentClientCapabilities enables per-feature capabilities.
type TextDocumentClientCapabilities struct {
	Definition     *struct{} `json:"definition,omitempty"`
	References     *struct{} `json:"references,omitempty"`
	DocumentSymbol *struct{} `json:"documentSymbol,omitempty"`
	Hover          *struct{} `json:"hover,omitempty"`
	Rename         *struct{} `json:"rename,omitempty"`
	CodeAction     *struct{} `json:"codeAction,omitempty"`
	PublishDiagnostics *struct{} `json:"publishDiagnostics,omitempty"`
}

// TextDocumentIdentifier identifies a document by URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextDocumentPositionParams is a document + position, used by
// definition and references requests.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ReferenceParams extends TextDocumentPositionParams with context.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

// DocumentSymbolParams requests symbols for a document.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// Symbol is a document symbol (function, class, variable, etc.).
type Symbol struct {
	Name           string   `json:"name"`
	Kind           int      `json:"kind"`
	Range          Range    `json:"range"`
	SelectionRange Range    `json:"selectionRange"`
	Children       []Symbol `json:"children,omitempty"`
}

// HoverParams requests hover information at a position.
type HoverParams = TextDocumentPositionParams

// Hover is the result of a textDocument/hover request.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// MarkupContent is markdown or plaintext content.
type MarkupContent struct {
	Kind  string `json:"kind"` // "markdown" or "plaintext"
	Value string `json:"value"`
}

// CodeAction describes a refactoring or fix-it action.
type CodeAction struct {
	Title string `json:"title"`
	Kind  string `json:"kind,omitempty"`
	Edit  *WorkspaceEdit `json:"edit,omitempty"`
}

// WorkspaceEdit is the result of a rename — a set of edits per file.
type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes,omitempty"`
}

// TextEdit is a range replacement.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// Diagnostic is a compiler/linter message for a range.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// Diagnostic severities (LSP spec).
const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

// CodeActionParams requests code actions for a range.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      struct {
		Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
		Only         []string     `json:"only,omitempty"`
	} `json:"context"`
}

// RenameParams requests a symbol rename.
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName       string                `json:"newName"`
}

// LSP symbol kinds (subset of the LSP SymbolKind enum).
const (
	SymbolKindFile          = 1
	SymbolKindModule        = 2
	SymbolKindNamespace     = 3
	SymbolKindPackage       = 4
	SymbolKindClass         = 5
	SymbolKindMethod        = 6
	SymbolKindProperty      = 7
	SymbolKindField         = 8
	SymbolKindConstructor   = 9
	SymbolKindEnum          = 10
	SymbolKindInterface     = 11
	SymbolKindFunction      = 12
	SymbolKindVariable      = 13
	SymbolKindConstant      = 14
	SymbolKindString        = 15
	SymbolKindNumber        = 16
	SymbolKindBoolean       = 17
	SymbolKindArray         = 18
	SymbolKindObject        = 19
	SymbolKindKey           = 20
	SymbolKindNull          = 21
	SymbolKindEnumMember    = 22
	SymbolKindStruct        = 23
	SymbolKindEvent         = 24
	SymbolKindOperator      = 25
	SymbolKindTypeParameter = 26
)
