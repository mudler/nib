package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// cdpBackend converts a plain backend-node id (as stored in b.refs) to the
// cdproto type the DOM domain expects.
func cdpBackend(id int64) cdp.BackendNodeID { return cdp.BackendNodeID(id) }

// resolveRef looks up a snapshot ref (e.g. "@e5") in the most recent
// snapshot's ref map. Refs are per-snapshot: once a new browser_snapshot (or
// any tool that re-snapshots) runs, the old refs are gone.
func (b *browserServer) resolveRef(ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	b.mu.Lock()
	id, ok := b.refs[ref]
	b.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("unknown ref %q — take a fresh browser_snapshot; refs change each snapshot", ref)
	}
	return id, nil
}

// clickBackendNode resolves a backend DOM node to a JS handle, scrolls it into
// view, and clicks it via the DOM — the reliable path (a synthesized coordinate
// click can miss on scroll/overlay).
func (b *browserServer) clickBackendNode(ctx context.Context, backendID int64) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		obj, err := dom.ResolveNode().WithBackendNodeID(cdpBackend(backendID)).Do(ctx)
		if err != nil {
			return err
		}
		_, exc, err := runtime.CallFunctionOn(
			`function(){ this.scrollIntoView({block:"center"}); this.click(); return true; }`).
			WithObjectID(obj.ObjectID).WithReturnByValue(true).Do(ctx)
		if err != nil {
			return err
		}
		if exc != nil {
			return fmt.Errorf("click JS error: %s", exc.Text)
		}
		return nil
	}))
}

// typeIntoBackendNode resolves a backend DOM node, focuses it and clears any
// existing value, then inserts text via a real CDP input event
// (input.InsertText) so the DOM/renderer actually observes the keystrokes —
// unlike the accessibility path, which never reaches the DOM.
func (b *browserServer) typeIntoBackendNode(ctx context.Context, backendID int64, text string) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		obj, err := dom.ResolveNode().WithBackendNodeID(cdpBackend(backendID)).Do(ctx)
		if err != nil {
			return err
		}
		// Focus + clear existing value via the element handle.
		if _, exc, err := runtime.CallFunctionOn(
			`function(){ this.focus(); if('value' in this){ this.value=''; } this.dispatchEvent(new Event('input',{bubbles:true})); return true; }`).
			WithObjectID(obj.ObjectID).WithReturnByValue(true).Do(ctx); err != nil {
			return err
		} else if exc != nil {
			return fmt.Errorf("focus JS error: %s", exc.Text)
		}
		// Insert text as a real CDP input event so the DOM/renderer observes it
		// (unlike the AX path that never reaches the DOM).
		return input.InsertText(text).Do(ctx)
	}))
}
