package promise

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Match describes the sentence that holds a promise.
type Match struct {
	// Language is the language code of the pattern that matched: "en", "it",
	// "es", "fr" or "de".
	Language string
	// Sentence is the sentence as written, trimmed.
	Sentence string
}

const (
	// lastSentences is how many sentences at the end of the text are read.
	lastSentences = 4
	// maxTail is how much of the end of the text is read, in bytes.
	maxTail = 6000
)

// Promises reports whether text promises to check, wait, monitor, retry or
// move something later. See Find.
func Promises(text string) bool {
	_, ok := Find(text)
	return ok
}

// Find returns the first sentence, among the last few of text, that promises a
// later action. Languages are tried in the order English, Italian, Spanish,
// French, German.
func Find(text string) (Match, bool) {
	if len(text) > maxTail {
		text = text[len(text)-maxTail:]
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	sentences := splitSentences(strip(text))
	if len(sentences) > lastSentences {
		sentences = sentences[len(sentences)-lastSentences:]
	}
	for _, s := range sentences {
		if strings.HasSuffix(s, "?") || strings.Contains(s, "¿") {
			continue
		}
		for _, l := range languages {
			if l.matches(s) {
				return Match{Language: l.code, Sentence: strings.TrimSpace(strings.Trim(s, "*_ \t.!"))}, true
			}
		}
	}
	return Match{}, false
}

var (
	fencedCode   = regexp.MustCompile("(?s)```.*?(?:```|$)")
	inlineCode   = regexp.MustCompile("`[^`\n]*`")
	quotedText   = regexp.MustCompile(`"[^"\n]{0,400}"|“[^”\n]{0,400}”|«[^»\n]{0,400}»`)
	blockQuote   = regexp.MustCompile(`(?m)^[ \t]*>.*$`)
	sentenceEnds = regexp.MustCompile(`[.!?…]+(?:\s+|$)|[\n;]+`)
)

// strip removes what the assistant did not say in its own voice.
func strip(text string) string {
	text = fencedCode.ReplaceAllString(text, "\n")
	text = blockQuote.ReplaceAllString(text, "")
	text = inlineCode.ReplaceAllString(text, " ")
	text = quotedText.ReplaceAllString(text, " ")
	return text
}

// splitSentences cuts text at sentence ends and line breaks. A sentence that
// ends in a question mark keeps it.
func splitSentences(text string) []string {
	var out []string
	last := 0
	for _, loc := range sentenceEnds.FindAllStringIndex(text, -1) {
		end := loc[0]
		if strings.ContainsRune(text[loc[0]:loc[1]], '?') {
			end = loc[0] + strings.IndexByte(text[loc[0]:loc[1]], '?') + 1
		}
		if s := strings.TrimSpace(text[last:end]); s != "" {
			out = append(out, s)
		}
		last = loc[1]
	}
	if s := strings.TrimSpace(text[last:]); s != "" {
		out = append(out, s)
	}
	return out
}

// fold lower-cases s, removes accents and unifies apostrophes, so the patterns
// match text typed without accents too.
func fold(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'à', 'á', 'â', 'ä', 'ã', 'å':
			r = 'a'
		case 'è', 'é', 'ê', 'ë':
			r = 'e'
		case 'ì', 'í', 'î', 'ï':
			r = 'i'
		case 'ò', 'ó', 'ô', 'ö', 'õ':
			r = 'o'
		case 'ù', 'ú', 'û', 'ü':
			r = 'u'
		case 'ç':
			r = 'c'
		case 'ñ':
			r = 'n'
		case 'ß':
			b.WriteString("ss")
			continue
		case 'œ':
			b.WriteString("oe")
			continue
		case '’', '‘', '`', '´', 'ʼ':
			r = '\''
		}
		if r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// language holds the patterns of one language. All patterns run on folded
// text.
type language struct {
	code string
	// prep adjusts folded text before matching, or nil.
	prep func(string) string
	// must must match the sentence for any rule to apply, or nil.
	must *regexp.Regexp
	// opener matches a reassurance such as "don't worry". It is cut out of the
	// sentence before any other rule, so its negation does not cancel a
	// promise that follows it. It may be nil.
	opener *regexp.Regexp
	// trigger matches an action that waits for the person to tell or send
	// something ("tell me when it is finished and I will move the file"). The
	// person's message starts that action, so nothing is left to arm. It may be
	// nil.
	trigger *regexp.Regexp
	// skip rejects the sentence: offers, conditions on the user.
	skip *regexp.Regexp
	// negBefore matches the end of the text before a promise verb when the
	// negation applies to that verb ("non controllero"). negIn matches a
	// negation inside the promise or right after it, up to the next
	// punctuation (German "ich warte nicht"). Both may be nil. A negation
	// elsewhere in the sentence does not cancel the promise.
	negBefore *regexp.Regexp
	negIn     *regexp.Regexp
	// wait promises to wait. It does not count when the text after it says the
	// wait is for the user (userWait).
	wait     *regexp.Regexp
	userWait *regexp.Regexp
	// strong promises to monitor or come back, with no time word needed.
	strong *regexp.Regexp
	// weak is an ordinary action verb. It counts only with a time word.
	weak *regexp.Regexp
	time *regexp.Regexp
}

func (l *language) matches(sentence string) bool {
	s := fold(sentence)
	if l.prep != nil {
		s = l.prep(s)
	}
	if l.must != nil && !l.must.MatchString(s) {
		return false
	}
	if l.opener != nil {
		s = l.opener.ReplaceAllString(s, " ")
	}
	if l.trigger != nil && l.trigger.MatchString(s) {
		return false
	}
	if l.skip.MatchString(s) {
		return false
	}
	for _, loc := range l.wait.FindAllStringIndex(s, -1) {
		if !l.userWait.MatchString(s[loc[1]:]) && !l.negated(s, loc) {
			return true
		}
	}
	if l.live(l.strong, s) {
		return true
	}
	return l.live(l.weak, s) && l.time.MatchString(s)
}

// live reports whether re matches s at a place where the promise verb is not
// negated.
func (l *language) live(re *regexp.Regexp, s string) bool {
	for _, loc := range re.FindAllStringIndex(s, -1) {
		if !l.negated(s, loc) {
			return true
		}
	}
	return false
}

// negated reports whether the match at loc is negated.
func (l *language) negated(s string, loc []int) bool {
	if l.negBefore != nil && l.negBefore.MatchString(s[:loc[0]]) {
		return true
	}
	if l.negIn != nil {
		end := loc[1]
		if i := strings.IndexAny(s[end:], ",;.:"); i >= 0 {
			s = s[:end+i]
		}
		if len(s) > end+24 {
			s = s[:end+24]
		}
		return l.negIn.MatchString(s[loc[0]:])
	}
	return false
}

// re compiles the parts joined with "|" inside one group.
func re(parts ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?:` + strings.Join(parts, "|") + `)`)
}

var languages = []*language{english, italian, spanish, french, german}
