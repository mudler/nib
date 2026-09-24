package chat

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// extractToolImages pulls image content from a cogito ToolStatus.ResultData
// (which holds *mcp.CallToolResult) as ToolImage values carrying raw bytes.
//
// This mirrors cogito's imagesFromResultData but keeps raw bytes instead of
// building data-URI strings. cogito still does its own image forwarding to
// the model; this is a parallel path that carries bytes to the TUI for
// inline rendering.
func extractToolImages(resultData any) []ToolImage {
	res, ok := resultData.(*mcp.CallToolResult)
	if !ok || res == nil {
		return nil
	}
	var out []ToolImage
	id := 0
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok && len(img.Data) > 0 {
			mime := img.MIMEType
			if mime == "" {
				mime = "image/png"
			}
			id++
			out = append(out, ToolImage{
				ID:   id,
				Data: img.Data,
				MIME: mime,
			})
		}
	}
	return out
}
