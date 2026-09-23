package theme

import (
	"math"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
)

// Motion: the few animated inks. Each is a blend between two palette inks,
// computed in Lab space so the midpoints do not go muddy. The result is a
// hex color; lipgloss degrades it to the nearest ANSI 256 or 16 color on a
// terminal without true color, so the animation still steps there.

// Blend returns the ink t of the way from a to b, with t clamped to [0, 1].
// a and b are palette inks (ANSI 256 codes, as in the Inks block).
func Blend(a, b lipgloss.Color, t float64) lipgloss.Color {
	switch {
	case t <= 0:
		return a
	case t >= 1:
		return b
	}
	ca, okA := inkRGB(a)
	cb, okB := inkRGB(b)
	if !okA || !okB {
		return b
	}
	return lipgloss.Color(ca.BlendLab(cb, t).Clamped().Hex())
}

// inkRGB converts a palette ink (an ANSI 256 code) to RGB.
func inkRGB(c lipgloss.Color) (colorful.Color, bool) {
	n, err := strconv.Atoi(string(c))
	if err != nil || n < 0 || n > 255 {
		return colorful.Color{}, false
	}
	return termenv.ConvertToRGB(termenv.ANSI256Color(n)), true
}

// CursorPulsePeriod is one full breath of the streaming cursor.
const CursorPulsePeriod = 1200 * time.Millisecond

// StreamCursorAt renders the streaming cursor at elapsed time since the reply
// started. It breathes between Faint and Accent on a sine, so it reads as
// alive without blinking hard.
func StreamCursorAt(elapsed time.Duration) string {
	return lipgloss.NewStyle().Foreground(pulseInk(elapsed)).Render(StreamCursor)
}

// RunningDotAt renders the mark of a tool block whose call is still running,
// breathing on the same sine as the streaming cursor.
func RunningDotAt(elapsed time.Duration) string {
	return lipgloss.NewStyle().Foreground(pulseInk(elapsed)).Render(RunningDot)
}

// pulseInk is the ink at elapsed time into a breath between Faint and Accent.
func pulseInk(elapsed time.Duration) lipgloss.Color {
	phase := float64(elapsed%CursorPulsePeriod) / float64(CursorPulsePeriod)
	t := (1 - math.Cos(2*math.Pi*phase)) / 2
	return Blend(Faint, Accent, 0.35+0.65*t)
}

// FadeDuration is how long a new transcript entry takes to reach its full ink.
const FadeDuration = 240 * time.Millisecond

// FadeInk returns ink for an entry that is arriving (1 just arrived, 0 fully
// in): it eases out from Faint to ink, fast at first and settling gently.
func FadeInk(ink lipgloss.Color, arriving float64) lipgloss.Color {
	if arriving <= 0 {
		return ink
	}
	t := 1 - arriving
	t = 1 - (1-t)*(1-t)*(1-t) // ease-out cubic
	return Blend(Faint, ink, t)
}

// Fading returns style with its foreground faded by arriving (see FadeInk).
// A style with no foreground ink, or an entry fully in, comes back unchanged.
func Fading(style lipgloss.Style, arriving float64) lipgloss.Style {
	if arriving <= 0 {
		return style
	}
	ink, ok := style.GetForeground().(lipgloss.Color)
	if !ok {
		return style
	}
	return style.Foreground(FadeInk(ink, arriving))
}

// sparkBars are the eighth-block bars a sparkline is drawn with.
var sparkBars = []rune("▁▂▃▄▅▆▇█")

// Sparkline draws values as a row of bars scaled to the largest of them.
// It returns "" on a terminal that cannot draw eighth blocks (see
// RestrictedGlyphs), or when every value is 0.
func Sparkline(values []float64) string {
	if RestrictedGlyphs() {
		return ""
	}
	var top float64
	for _, v := range values {
		top = math.Max(top, v)
	}
	if top <= 0 {
		return ""
	}
	out := make([]rune, len(values))
	for i, v := range values {
		n := int(math.Round(math.Max(0, v) / top * float64(len(sparkBars)-1)))
		out[i] = sparkBars[n]
	}
	return string(out)
}
