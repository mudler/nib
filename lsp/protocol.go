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
