package watch

// Snapshot is the last observation a caller keeps between checks. The zero
// value means nothing was observed yet.
type Snapshot struct {
	Hash   string
	Output string
}

// Kind is what a check means against the previous snapshot.
type Kind int

const (
	// Failed means the check could not be made. It is an error, never a
	// change, and the previous snapshot stays valid.
	Failed Kind = iota
	// Baseline means this is the first successful check. There is nothing to
	// compare with, so it is not a change.
	Baseline
	// Unchanged means the output matches the previous snapshot.
	Unchanged
	// Changed means the output differs from the previous snapshot.
	Changed
)

func (k Kind) String() string {
	switch k {
	case Failed:
		return "failed"
	case Baseline:
		return "baseline"
	case Unchanged:
		return "unchanged"
	case Changed:
		return "changed"
	}
	return "unknown"
}

// Outcome is the verdict of Compare.
type Outcome struct {
	Kind Kind
	// Err is the check error when Kind is Failed.
	Err error
	// Snapshot is the snapshot to store. It is prev when Kind is Failed or
	// Unchanged.
	Snapshot Snapshot
	// Diff is the unified diff of a Changed outcome, cut to the maxDiff given
	// to Compare.
	Diff string
}

// Compare decides what res means against prev. Store Outcome.Snapshot before
// you act on a Changed outcome, so a failure in your action does not report
// the same change again. maxDiff cuts Outcome.Diff, see Diff.
func Compare(prev Snapshot, res Result, maxDiff int) Outcome {
	if res.Err != nil {
		return Outcome{Kind: Failed, Err: res.Err, Snapshot: prev}
	}
	next := Snapshot{Hash: res.Hash, Output: res.Output}
	switch {
	case prev.Hash == "":
		return Outcome{Kind: Baseline, Snapshot: next}
	case prev.Hash == res.Hash:
		return Outcome{Kind: Unchanged, Snapshot: prev}
	}
	return Outcome{Kind: Changed, Snapshot: next, Diff: Diff(prev.Output, res.Output, maxDiff)}
}
