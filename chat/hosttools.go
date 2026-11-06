package chat

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// hostTransport marks a transport whose tools belong to the program that embeds
// nib, not to the user's configuration.
type hostTransport struct{ mcp.Transport }

// HostTransport marks t as the tool server of the embedding host. Pass the
// result to NewSession like any other transport. Its tools differ from those of
// other transports in two ways:
//
//   - They are not gated by Config.BuiltinTools. That list switches nib's own
//     built-in tools on; it does not have to name the host's tools too.
//   - A host tool wins over a built-in of the same name: the built-in is not
//     registered, so the model is never offered two tools of one name.
//
// A host that wants a built-in to behave its own way sets the matching
// Callbacks field (OnWaitFor, OnCronCreate, ...) instead of replacing the tool.
func HostTransport(t mcp.Transport) mcp.Transport { return hostTransport{t} }

// hostToolNames lists the tools a host session serves.
func hostToolNames(ctx context.Context, sess *mcp.ClientSession) []string {
	var names []string
	var cursor string
	for {
		res, err := sess.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return names
		}
		for _, t := range res.Tools {
			names = append(names, t.Name)
		}
		if res.NextCursor == "" {
			return names
		}
		cursor = res.NextCursor
	}
}
