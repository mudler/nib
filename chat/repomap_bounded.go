package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/mudler/nib/codeindex"
)

type repoMapWorker interface {
	ParseSource(context.Context, string, []byte) ([]codeindex.Entry, bool, error)
	Close() error
}

func newRepoMapWorker(ctx context.Context) repoMapWorker { return codeindex.NewWorker(ctx) }

type repoMapLimits struct {
	files, scanned int
	bytes          int64
	duration       time.Duration
}

var repoMapLimitsDefault = repoMapLimits{1000, 20000, 16 << 20, 10 * time.Second}

type mapScan struct {
	retained                    int
	ctx                         context.Context
	root                        string
	limits                      repoMapLimits
	scanned, attempted, skipped int
	bytes                       int64
	reason                      string
	entries                     []repoMapFileEntry
	worker                      repoMapWorker
	supported                   map[string]bool
}

func (s *mapScan) stop() bool {
	if s.ctx.Err() != nil {
		s.reason = "overall deadline"
		return true
	}
	return s.reason != ""
}
func (s *mapScan) scan() bool {
	if s.stop() {
		return false
	}
	if s.scanned >= s.limits.scanned {
		s.reason = "entry limit"
		return false
	}
	s.scanned++
	return true
}

// read accounts actual bytes (including ignore files), not just stat sizes.
// Opening without following links and in nonblocking mode also handles a file
// replaced by a FIFO between discovery and open.
func (s *mapScan) read(path string) ([]byte, error) {
	if s.stop() {
		return nil, s.ctx.Err()
	}
	f, err := openRepoMapRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > codeindex.WorkerMaxSourceBytes {
		return nil, errors.New("oversize file")
	}
	remaining := s.limits.bytes - s.bytes
	if remaining <= 0 {
		s.reason = "byte limit"
		return nil, errors.New(s.reason)
	}
	limit := int64(min(codeindex.WorkerMaxSourceBytes, int(remaining)))
	// Chunked reads allow cancellation between regular-file reads.
	var data []byte
	buf := make([]byte, 32<<10)
	for int64(len(data)) < limit {
		if s.stop() {
			return nil, s.ctx.Err()
		}
		n, e := f.Read(buf[:min(len(buf), int(limit)-len(data))])
		s.bytes += int64(n)
		data = append(data, buf[:n]...)
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if after.Size() > int64(len(data)) {
		if s.bytes >= s.limits.bytes {
			s.reason = "byte limit"
		}
		return nil, errors.New("file grew or input limit")
	}
	return data, nil
}
func (s *mapScan) visit(rel string) bool {
	if s.stop() {
		return false
	}
	// Git paths and fallback paths must stay below the requested directory.
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		s.skipped++
		return true
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	p := s.root
	for i, part := range parts {
		p = filepath.Join(p, part)
		if i < len(parts)-1 {
			if part == ".git" || part == ".hg" || part == ".svn" {
				s.skipped++
				return true
			}
			fi, err := os.Lstat(p)
			if err != nil || !fi.IsDir() {
				s.skipped++
				return true
			}
			if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				s.skipped++
				return true
			}
		}
	}
	ext := strings.ToLower(filepath.Ext(rel))
	base := strings.ToLower(filepath.Base(rel))
	if !s.supported[ext] && !(ext == "" && s.supported[base]) {
		return true
	}
	if s.attempted >= s.limits.files {
		s.reason = "file limit"
		return false
	}
	s.attempted++
	src, err := s.read(p)
	if err != nil {
		s.skipped++
		return !s.stop()
	}
	raw, _, err := s.worker.ParseSource(s.ctx, p, src)
	if err != nil {
		s.skipped++
		s.reason = "parser worker failed or parse deadline"
		return false
	}
	kept := filterRepoMapEntries(raw)
	// Bound retained extraction data independently of source and wire framing.
	for _, e := range kept {
		s.retained += len(e.Name) + len(e.Detail) + len(e.Section) + 128
		for _, field := range e.Fields {
			s.retained += len(field) + 16
		}
		if s.retained > 8<<20 {
			s.reason = "definition memory limit"
			s.skipped++
			return false
		}
	}
	if len(kept) > 0 {
		s.entries = append(s.entries, repoMapFileEntry{path: p, entries: kept, defCount: len(kept), size: int64(len(src))})
	}
	return !s.stop()
}

// Git performs standard/global/nested ignore handling and preserves tracked
// ignored files. NUL records are consumed incrementally; neither stdout nor
// stderr is captured into an unbounded buffer. No submodule recursion.
func (s *mapScan) git() bool {
	cmd := exec.CommandContext(s.ctx, "git", "-C", s.root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ".")
	cmd.WaitDelay = 100 * time.Millisecond
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	if err = cmd.Start(); err != nil {
		return false
	}
	defer pipe.Close()
	stopRead := context.AfterFunc(s.ctx, func() { _ = pipe.Close() })
	defer stopRead()
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 4096), 4097)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, c := range data {
			if c == 0 {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return 0, nil, errors.New("unterminated Git path")
		}
		return 0, nil, nil
	})
	seen := make(map[string]bool) // At most the entry cap, never the entire tree.
	received := false
	for scanner.Scan() {
		received = true
		if !s.scan() {
			break
		}
		rel := filepath.FromSlash(scanner.Text())
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if !s.visit(rel) {
			break
		}
	}
	if scanner.Err() != nil {
		s.reason = "Git discovery record/read limit"
	}
	if s.reason != "" || s.ctx.Err() != nil {
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	if err != nil && received && s.reason == "" {
		s.reason = "Git discovery failed"
	}
	return received || err == nil || s.stop()
}

// Fallback uses go-git's maintained gitignore matcher, with scoped patterns,
// rather than interpreting Git patterns as filepath globs. Directory reads are
// batched (WalkDir would buffer a whole huge directory before checking caps).
func (s *mapScan) walk(rel string, patterns []gitignore.Pattern, depth int) {
	if s.stop() {
		return
	}
	if depth > 128 {
		s.reason = "directory depth limit"
		return
	}
	path := filepath.Join(s.root, rel)
	f, err := openRepoMapDirectory(path)
	if err != nil {
		s.skipped++
		return
	}
	defer f.Close()
	ignore := filepath.Join(path, ".gitignore")
	if fi, e := os.Lstat(ignore); e == nil && fi.Mode().IsRegular() {
		data, e := s.read(ignore)
		if e != nil {
			s.skipped++
			if s.reason == "" {
				s.reason = "ignore file unreadable or oversized"
			}
			return
		}
		var domain []string
		if rel != "" {
			domain = strings.Split(filepath.ToSlash(rel), "/")
		}
		// Give children independent backing storage so sibling rules cannot leak.
		patterns = append([]gitignore.Pattern(nil), patterns...)
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if line != "" && !strings.HasPrefix(line, "#") {
				patterns = append(patterns, gitignore.ParsePattern(line, domain))
			}
		}
	}
	matcher := gitignore.NewMatcher(patterns)
	for !s.stop() {
		entries, e := f.ReadDir(1)
		for _, d := range entries {
			if !s.scan() {
				return
			}
			child := filepath.Join(rel, d.Name())
			if d.Type()&os.ModeSymlink != 0 {
				s.skipped++
				continue
			}
			if matcher.Match(strings.Split(filepath.ToSlash(child), "/"), d.IsDir()) {
				s.skipped++
				continue
			}
			if d.IsDir() {
				if repoMapSkipDirs[d.Name()] {
					s.skipped++
					continue
				}
				if _, e := os.Lstat(filepath.Join(s.root, child, ".git")); e == nil {
					s.skipped++
					continue
				}
				s.walk(child, patterns, depth+1)
			} else if d.Type().IsRegular() {
				if !s.visit(child) {
					return
				}
			} else {
				s.skipped++
			}
		}
		if e == io.EOF {
			return
		}
		if e != nil {
			s.skipped++
			return
		}
	}
}

func renderRepoMapWith(parent context.Context, root string, budget int, factory func(context.Context) repoMapWorker, limits repoMapLimits) (string, error) {
	ctx, cancel := context.WithTimeout(parent, limits.duration)
	defer cancel()
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if budget <= 0 {
		budget = repoMapDefaultBudget
	}
	budget = min(budget, repoMapMaxBudget)
	s := &mapScan{ctx: ctx, root: root, limits: limits, supported: map[string]bool{}}
	for _, ext := range codeindex.SupportedExtensions() {
		s.supported[ext] = true
	}
	s.worker = factory(ctx)
	defer s.worker.Close()
	if !s.stop() {
		f, e := openRepoMapDirectory(root)
		if e != nil {
			return "", e
		}
		f.Close()
		if !s.git() {
			s.walk("", nil, 0)
		}
	}
	if parent.Err() != nil {
		s.reason = "user cancelled"
	} else if ctx.Err() != nil {
		s.reason = "overall deadline"
	}
	sort.Slice(s.entries, func(i, j int) bool {
		a, b := s.entries[i], s.entries[j]
		if a.defCount != b.defCount {
			return a.defCount > b.defCount
		}
		if a.size != b.size {
			return a.size > b.size
		}
		return a.path < b.path
	})
	return s.render(budget * 4), nil
}

func (s *mapScan) render(bytes int) string {
	blocks := make([]string, len(s.entries))
	for i, e := range s.entries {
		blocks[i] = renderRepoMapFile(e)
	}
	shown := 0
	used := 0
	footer := func(n int) string {
		omitted := len(blocks) - n
		if s.reason == "" && s.skipped == 0 && omitted == 0 {
			return ""
		}
		reason := s.reason
		if reason == "" {
			if omitted > 0 {
				reason = "output budget"
			} else {
				reason = "skipped files/trees"
			}
		}
		return fmt.Sprintf("... (%d more file(s) not shown)\nPartial: %s; scanned=%d attempted=%d read=%dB skipped=%d. Narrow path or use index.\n", omitted, reason, s.scanned, s.attempted, s.bytes, s.skipped)
	}
	for shown < len(blocks) {
		extra := len(blocks[shown])
		if shown > 0 {
			extra++
		}
		if used+extra+len(footer(shown+1)) > bytes {
			break
		}
		used += extra
		shown++
	}
	tail := footer(shown)
	if len(tail) > bytes {
		reason := s.reason
		if reason == "" {
			reason = "output budget"
		}
		tail = fmt.Sprintf("Partial: %s; skipped=%d omitted=%d. Narrow path.\n", reason, s.skipped, len(blocks))
		if len(tail) > bytes {
			tail = tail[:bytes]
		}
		return tail
	}
	return strings.Join(blocks[:shown], "\n") + tail
}
