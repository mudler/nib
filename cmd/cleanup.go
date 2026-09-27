package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/plugin"
)

// cleanupOptions selects the recorded sessions `nib cleanup` deletes. A
// session goes when any selector matches it; with none set, nothing goes.
type cleanupOptions struct {
	all       bool          // every session in scope
	olderThan time.Duration // not updated for longer than this; 0 is off
	keep      int           // keep the newest keep sessions in scope; -1 is off
}

func (o cleanupOptions) any() bool {
	return o.all || o.olderThan > 0 || o.keep >= 0
}

// selectForCleanup returns the sessions opts deletes. sessions must be newest
// first, as SessionStore.List returns them.
func selectForCleanup(sessions []chat.SessionRecord, now time.Time, opts cleanupOptions) []chat.SessionRecord {
	var out []chat.SessionRecord
	for i, s := range sessions {
		switch {
		case opts.all,
			opts.olderThan > 0 && now.Sub(s.Updated) > opts.olderThan,
			opts.keep >= 0 && i >= opts.keep:
			out = append(out, s)
		}
	}
	return out
}

// parseAge reads a --older-than value: a Go duration ("12h", "90m") or a
// whole number of days ("30d") or weeks ("2w"), which time.ParseDuration
// does not accept.
func parseAge(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(v, suffix); ok {
			days, err := strconv.Atoi(n)
			if err != nil || days <= 0 {
				return 0, fmt.Errorf("invalid age %q", v)
			}
			return time.Duration(days) * unit, nil
		}
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid age %q (use e.g. 30d, 2w or 12h)", v)
	}
	return d, nil
}

// staleTempAge is how old a leftover SessionStore temp file must be before
// cleanup removes it. A younger one can belong to a save still in progress
// in a running nib.
const staleTempAge = time.Hour

// RunCleanupCommand implements `nib cleanup`: it deletes recorded /resume
// sessions under <base>/sessions by age or count, and removes temp files an
// interrupted save left behind.
func RunCleanupCommand(programName, baseDir string, args []string) int {
	return runCleanup(runnableName(programName), filepath.Join(plugin.BaseDirIn(baseDir), "sessions"), args, time.Now(), os.Stdout, os.Stderr)
}

func runCleanup(prog, dir string, args []string, now time.Time, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(prog+" cleanup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "Delete every recorded session")
	olderThan := fs.String("older-than", "", "Delete sessions not updated for this long (e.g. 30d, 2w, 12h)")
	keep := fs.Int("keep", -1, "Keep only the N most recently updated sessions")
	here := fs.Bool("here", false, "Only consider sessions recorded in the current directory")
	dryRun := fs.Bool("dry-run", false, "List what would be deleted without deleting it")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s cleanup [--older-than AGE] [--keep N] [--all] [--here] [--dry-run]\n\n", prog)
		fmt.Fprintf(stderr, "Delete recorded sessions (the ones /resume lists) from %s.\n", dir)
		fmt.Fprintf(stderr, "A session is deleted when any of --all, --older-than or --keep selects it.\n")
		fmt.Fprintf(stderr, "With no selector, it shows how many sessions there are and deletes nothing.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s cleanup: unexpected argument %q\n", prog, fs.Arg(0))
		fs.Usage()
		return 2
	}

	opts := cleanupOptions{all: *all, keep: *keep}
	if *olderThan != "" {
		d, err := parseAge(*olderThan)
		if err != nil {
			fmt.Fprintf(stderr, "%s cleanup: --older-than: %v\n", prog, err)
			return 2
		}
		opts.olderThan = d
	}
	if *keep < -1 {
		fmt.Fprintf(stderr, "%s cleanup: --keep must be 0 or more\n", prog)
		return 2
	}

	cwd := ""
	if *here {
		cwd, _ = os.Getwd()
	}
	store := chat.NewSessionStore(dir)
	sessions, err := store.List(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "%s cleanup: %v\n", prog, err)
		return 1
	}

	if !opts.any() {
		var total int64
		for _, s := range sessions {
			total += fileSize(store.Path(s.ID))
		}
		fmt.Fprintf(stdout, "%d recorded sessions, %s, in %s\n", len(sessions), humanBytes(total), dir)
		if len(sessions) > 0 {
			fmt.Fprintf(stdout, "Oldest updated %s. Run '%s cleanup --help' to delete by age or count.\n",
				sessions[len(sessions)-1].Updated.Format("2006-01-02"), prog)
		}
		return 0
	}

	doomed := selectForCleanup(sessions, now, opts)
	var freed int64
	failed := 0
	for _, s := range doomed {
		size := fileSize(store.Path(s.ID))
		title := s.Title
		if title == "" {
			title = "(untitled session)"
		}
		if *dryRun {
			fmt.Fprintf(stdout, "would delete %s  %s  %s\n", s.ID, s.Updated.Format("2006-01-02"), title)
			freed += size
			continue
		}
		if err := store.Delete(s.ID); err != nil {
			fmt.Fprintf(stderr, "%s cleanup: %v\n", prog, err)
			failed++
			continue
		}
		freed += size
	}
	if !*dryRun {
		removeStaleTemps(dir, now)
	}

	verb := "Deleted"
	if *dryRun {
		verb = "Would delete"
	}
	fmt.Fprintf(stdout, "%s %d of %d sessions, %s.\n", verb, len(doomed)-failed, len(sessions), humanBytes(freed))
	if failed > 0 {
		return 1
	}
	return 0
}

// removeStaleTemps deletes SessionStore temp files ("."+id+"-*.tmp") older
// than staleTempAge: what a save killed between write and rename leaves.
func removeStaleTemps(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > staleTempAge {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
