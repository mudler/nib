package types

// PruningState is how far a session had shrunk its tool results in the
// requests it sent: the results replaced by a stub, and the results shortened
// by progressive compression. A recorded session keeps it, so the first
// request after a resume stubs and shortens the same results as the last
// request before the quit. Keys are tool_call_ids in the stored context.
type PruningState struct {
	// Pruned maps each stubbed result to the clause its stub carries.
	Pruned map[string]string `json:"pruned,omitempty"`
	// Compressed maps each shortened result to its level and text.
	Compressed map[string]CompressedResult `json:"compressed,omitempty"`
	// CompressBand is the context-pressure band of the last request.
	CompressBand int `json:"compress_band,omitempty"`
}

// CompressedResult is one tool result as progressive compression rendered it.
type CompressedResult struct {
	Level   int    `json:"level"`
	Content string `json:"content"`
}
