package render

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// FitWidth breaks every line of s wider than width into lines that fit,
// keeping all of its text.
//
// It exists because the transcript viewport clips what it cannot fit: its
// View applies MaxWidth, which cuts a line at the viewport edge and drops the
// rest. A word-wrapper leaves a token longer than the width (a URL, a long
// path) on one over-wide line, so without this the tail of that token was
// lost from the screen. A break inside the token is better than a gap in it.
func FitWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[i] = ansi.Hardwrap(line, width, true)
		}
	}
	return strings.Join(lines, "\n")
}

var urlRe = regexp.MustCompile(`^https?://\S+$`)

// linkSeg is one run of a wrapped line: text, and the URL it is part of
// ("" for plain text).
type linkSeg struct {
	text string
	url  string
}

// WrapLinked word-wraps plain text to width, keeping its own line breaks and
// the leading indent of each line, and makes every URL in it a terminal
// hyperlink (OSC 8).
//
// A URL longer than the width is split over several lines, and each piece
// is its own hyperlink to the WHOLE URL, with one shared id. A click on any
// piece opens the full URL, terminals that group links by id highlight the
// pieces together, and a piece that scrolled off the top does not take the
// link with it. Terminals without OSC 8 support ignore the sequences and
// show the text as before.
//
// Use it for text that must reach the user exactly as written, such as a
// login prompt: markdown rendering would reflow its lines and strip
// <placeholders> as HTML.
func WrapLinked(text string, width int) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, wrapLinkedLine(line, width)...)
	}
	return strings.Join(out, "\n")
}

func wrapLinkedLine(line string, width int) []string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
	words := strings.Fields(line)
	if len(words) == 0 || width <= 0 {
		return []string{renderSegs([]linkSeg{{text: line}})}
	}
	if ansi.StringWidth(indent) >= width {
		indent = ""
	}

	var lines []string
	cur := []linkSeg{{text: indent}}
	curW := ansi.StringWidth(indent)
	hasWord := false
	flush := func() {
		lines = append(lines, renderSegs(cur))
		cur, curW, hasWord = nil, 0, false
	}
	for _, w := range words {
		url := ""
		if urlRe.MatchString(w) {
			url = w
		}
		ww := ansi.StringWidth(w)
		sep := 0
		if hasWord {
			sep = 1
		}
		switch {
		case curW+sep+ww <= width:
			if sep == 1 {
				cur = append(cur, linkSeg{text: " "})
			}
		case ww <= width:
			flush()
		default:
			// Longer than a whole line: start it on its own line and cut it
			// into width-sized pieces, the last of which stays open for
			// the words after it.
			if hasWord {
				flush()
			}
			for _, piece := range splitWidth(w, width-curW, width) {
				if hasWord {
					flush()
				}
				cur = append(cur, linkSeg{text: piece, url: url})
				curW += ansi.StringWidth(piece)
				hasWord = true
			}
			continue
		}
		cur = append(cur, linkSeg{text: w, url: url})
		curW += sep + ww
		hasWord = true
	}
	flush()
	return lines
}

// splitWidth cuts s into pieces: the first at most first cells wide, the
// rest at most width.
func splitWidth(s string, first, width int) []string {
	if first <= 0 {
		first = width
	}
	var pieces []string
	var b strings.Builder
	limit, used := first, 0
	for _, r := range s {
		rw := ansi.StringWidth(string(r))
		if used+rw > limit && used > 0 {
			pieces = append(pieces, b.String())
			b.Reset()
			limit, used = width, 0
		}
		b.WriteRune(r)
		used += rw
	}
	return append(pieces, b.String())
}

func renderSegs(segs []linkSeg) string {
	var b strings.Builder
	for _, s := range segs {
		if s.url == "" {
			b.WriteString(s.text)
			continue
		}
		b.WriteString(ansi.SetHyperlink(s.url, "id="+linkID(s.url)))
		b.WriteString(s.text)
		b.WriteString(ansi.ResetHyperlink())
	}
	return b.String()
}

// linkID is the OSC 8 id shared by the pieces of one URL.
func linkID(url string) string {
	h := fnv.New32a()
	h.Write([]byte(url))
	return fmt.Sprintf("nib-%08x", h.Sum32())
}
