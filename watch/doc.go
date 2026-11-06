// Package watch checks an external source for change.
//
// A [Source] is a shell command or a URL. [Run] fetches it once and returns
// the capped output with its SHA-256 hash. [Compare] decides what a check
// means against the previous [Snapshot], and [Diff] renders what changed. A
// [Waiter] turns repeated checks into one verdict: the output changed, it
// matched a pattern, or a script exited with status 0, with a deadline and a
// limit on consecutive failures so that silence is never mistaken for success.
//
// A Waiter can be saved with [Waiter.State] and resumed with [RestoreWaiter],
// so a host can survive a restart or move a wait to another node.
//
// The package keeps no state of its own and has no dependency on a session, a
// UI or a scheduler. The caller owns the polling loop and the stored
// [Snapshot], so any host can embed it.
//
//	var prev watch.Snapshot
//	for range time.Tick(time.Minute) {
//		res := watch.Run(ctx, watch.Source{URL: "https://example.com/status"})
//		out := watch.Compare(prev, res, 2000)
//		switch out.Kind {
//		case watch.Failed:
//			log.Println("check failed:", out.Err) // prev is kept
//		case watch.Changed:
//			prev = out.Snapshot // store before you act on it
//			fmt.Print(out.Diff)
//		case watch.Baseline:
//			prev = out.Snapshot // first success: nothing to report yet
//		}
//	}
package watch
