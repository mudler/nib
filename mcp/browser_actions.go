package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// pressAllowedKeys is the allowlist of key names accepted by browser_press,
// mapped to the corresponding github.com/chromedp/chromedp/kb control-rune
// constant. kb's named constants (kb.Enter, kb.Tab, ...) are strings holding
// the single Unicode "control" rune chromedp's key-encoding table maps back
// to the real Chrome DOM key (see kb.Keys) — that's what chromedp.KeyEvent
// expects, not a literal key name.
var pressAllowedKeys = map[string]string{
	"Enter":      kb.Enter,
	"Tab":        kb.Tab,
	"Escape":     kb.Escape,
	"ArrowUp":    kb.ArrowUp,
	"ArrowDown":  kb.ArrowDown,
	"ArrowLeft":  kb.ArrowLeft,
	"ArrowRight": kb.ArrowRight,
	"Backspace":  kb.Backspace,
	"Delete":     kb.Delete,
	"Home":       kb.Home,
	"End":        kb.End,
	"PageUp":     kb.PageUp,
	"PageDown":   kb.PageDown,
}

// keyForName maps an allowed browser_press key name to the kb control-rune
// string chromedp.KeyEvent expects. The error names the valid values so the
// model can retry with a correct one.
func keyForName(name string) (string, error) {
	if k, ok := pressAllowedKeys[name]; ok {
		return k, nil
	}
	return "", fmt.Errorf("browser_press: unsupported key %q — valid keys: Enter, Tab, Escape, ArrowUp, ArrowDown, ArrowLeft, ArrowRight, Backspace, Delete, Home, End, PageUp, PageDown", name)
}

const (
	pressSettleMaxProbes = 8 // ~1.2s ceiling for a triggered navigation to finish loading
	pressSettleNavGrace  = 2 // ~300ms to decide a key triggered NO navigation
	pressSettleInterval  = 150 * time.Millisecond
)

// settleAfterKey gives a key that may have triggered a native navigation
// (Enter submitting a form, Backspace on some SPAs, ...) a brief, bounded
// chance to actually kick off and finish loading before the caller
// re-snapshots. Without this the re-snapshot describes stale pre-navigation
// DOM: chromedp's key dispatch returns before the browser even starts the
// navigation, so at that instant the ORIGINAL document is still
// readyState=="complete" — an unconditional "is it complete?" check returns
// true on the page we're trying to leave (verified against a real Chrome: an
// immediate re-snapshot after chromedp.KeyEvent(kb.Enter) caught the old page,
// and a single readyState==complete check returned on it too).
//
// beforeURL is location.href captured BEFORE the key was dispatched. A
// navigating key changes the URL (or transiently tears down the JS execution
// context mid-load); we wait until the URL actually changes AND the new
// document reaches "complete". A key that navigates nowhere (arrows, Tab,
// Escape) never changes the URL, so once a short grace passes with no sign of
// navigation we return immediately rather than stalling the full ceiling.
// Best-effort throughout: a possibly-stale snapshot is a worse outcome than a
// broken tool call only in that the model can just re-snapshot.
func settleAfterKey(ctx context.Context, beforeURL string) {
	navSeen := false
	for probe := 0; probe < pressSettleMaxProbes; probe++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pressSettleInterval):
		}
		var href, readyState string
		if err := chromedp.Run(ctx,
			chromedp.Evaluate("location.href", &href),
			chromedp.Evaluate("document.readyState", &readyState),
		); err != nil {
			navSeen = true // execution context torn down => a navigation is underway
			continue
		}
		if href != beforeURL {
			navSeen = true
			if readyState == "complete" {
				return // navigation committed and the new document finished loading
			}
			continue // navigation in flight; wait for the new document to load
		}
		// Still on the original document. If nothing has hinted at a navigation
		// within the grace window, the key was a no-op (arrow/Tab/Escape) — return
		// rather than stalling to the ceiling. Enter's form submit commits (URL
		// change or context teardown) well inside this window.
		if readyState == "complete" && !navSeen && probe+1 >= pressSettleNavGrace {
			return
		}
	}
}

// browserImplicitSubmitJS reproduces the browser's default action for Enter in
// a focused form field: submit the form the active element belongs to. Returns
// "submitted" when it did, else "noform"/"noactive". requestSubmit (not the
// bare .submit()) is used so the submit event fires and constraint validation
// runs — exactly like a real Enter, so an onsubmit that preventDefaults still
// wins and a form with unfilled required fields is not submitted.
const browserImplicitSubmitJS = `(function(){
	var e=document.activeElement; if(!e) return 'noactive';
	var f=e.form; if(!f) return 'noform';
	if(f.requestSubmit){ f.requestSubmit(); } else { f.submit(); }
	return 'submitted';
})()`

// scrollDelta maps an allowed browser_scroll direction to the JS expression
// used as the y argument of window.scrollBy — positive (down the page) or
// negative (up the page) 90% of the viewport height, so one scroll leaves a
// little overlap with the previous view. The error names the valid values so
// the model can retry with a correct one.
func scrollDelta(direction string) (string, error) {
	switch direction {
	case "down":
		return "window.innerHeight*0.9", nil
	case "up":
		return "-window.innerHeight*0.9", nil
	default:
		return "", fmt.Errorf("browser_scroll: unsupported direction %q — valid directions: up, down", direction)
	}
}

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
		// Focus + clear existing value via the element handle. Form controls
		// (input/textarea) hold their text in .value; a contenteditable
		// element (AX role "textbox" but no .value) needs its textContent
		// cleared instead, or InsertText below just appends to what's there.
		if _, exc, err := runtime.CallFunctionOn(
			`function(){ this.focus(); if('value' in this){ this.value=''; } else if(this.isContentEditable){ this.textContent=''; } this.dispatchEvent(new Event('input',{bubbles:true})); return true; }`).
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
