// Package claudeplugin reads plugins and marketplaces that are compatible with
// the Claude Code plugin format, and returns them as plain data.
//
// The package does not run anything. It parses a plugin directory into a
// [Plugin]: its skills, commands, agents, MCP servers, hooks, LSP servers,
// bin/ executables and user configuration options. A host such as nib or an
// embedding server decides what to do with each part: map it to its own
// types, run hooks and MCP servers in its own executor or sandbox, or refuse
// it. Every component the parser cannot use is listed in [Plugin.Skipped]
// with a reason, so a host can show it to the user instead of dropping it in
// silence.
//
// The usual flow for a host:
//
//	mkt, err := claudeplugin.LoadMarketplace(marketplaceDir)
//	entry, err := mkt.Find("formatter")
//	res, err := claudeplugin.Fetch(ctx, mkt, entry, destDir, claudeplugin.FetchOptions{})
//	p, err := claudeplugin.Load(res.Dir, claudeplugin.LoadOptions{Entry: entry, Marketplace: mkt.Name})
//	if p.RunsCode() { /* needs an admin, or a sandbox */ }
//
// Text components keep their raw bodies. Call [ExpandContent] on a body to
// substitute ${CLAUDE_PLUGIN_ROOT} and the other variables, and
// [RenderArguments] on a command or skill body to substitute $ARGUMENTS, $N
// and $name. Shell injections (!`cmd`) and @file references are returned by
// [ParseInjections] as data; the package never runs them.
package claudeplugin
