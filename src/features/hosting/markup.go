package hosting

import (
	"bytes"
	"html/template"
	"log/slog"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"
)

// markdown renders fact content. Raw HTML and dangerous URLs are never passed
// through (goldmark escapes them unless html.WithUnsafe is set — it isn't).
// Hard wraps keep the plain-text behavior users expect: one newline in the
// source is one line break on the card.
var markdown = goldmark.New(
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// colorOpenRe matches the color span opener, e.g. "{#ff6600}".
var colorOpenRe = regexp.MustCompile(`\{#([0-9a-fA-F]{6})\}`)

const colorCloseTok = "{/}"

// furiganaRe matches a ruby annotation, e.g. "{漢字|かんじ}": a base run and a
// reading run separated by a pipe. Requiring the pipe keeps it disjoint from
// the color syntax ({#hex} / {/}), which never contains one. Braces, pipes and
// newlines are excluded from both runs so annotations can neither nest nor span
// lines.
var furiganaRe = regexp.MustCompile(`\{([^{}|\n]+)\|([^{}|\n]+)\}`)

// Sentinels are Unicode Private-Use-Area characters: inert to goldmark's
// inline parser (they're just ordinary text), so they survive markdown
// parsing (including inside bold/italic) undisturbed and get swapped for
// real markup only after goldmark has rendered the HTML.
const (
	colorOpenSentinel  = '\uE000'
	colorCloseSentinel = '\uE001'
	furiOpenSentinel   = '\uE002'
	furiMidSentinel    = '\uE003'
	furiCloseSentinel  = '\uE004'
)

var colorOpenSentinelRe = regexp.MustCompile(string(colorOpenSentinel) + `([0-9a-fA-F]{6})`)

var furiSentinelRe = regexp.MustCompile(
	string(furiOpenSentinel) + `(.*?)` + string(furiMidSentinel) + `(.*?)` + string(furiCloseSentinel))

// preprocessColor swaps "{#hex}...{/}" for sentinel markers goldmark will
// pass through untouched.
func preprocessColor(src string) string {
	src = colorOpenRe.ReplaceAllString(src, string(colorOpenSentinel)+"$1")
	return strings.ReplaceAll(src, colorCloseTok, string(colorCloseSentinel))
}

// postprocessColor swaps the sentinel markers left in rendered HTML for real
// <span style="color:#hex"> markup. The captured hex is constrained to
// [0-9a-fA-F]{6} by colorOpenSentinelRe, so it's safe to interpolate directly.
func postprocessColor(htmlOut string) string {
	htmlOut = colorOpenSentinelRe.ReplaceAllString(htmlOut, `<span style="color:#$1">`)
	return strings.ReplaceAll(htmlOut, string(colorCloseSentinel), "</span>")
}

// preprocessFurigana swaps "{base|reading}" for sentinel markers goldmark
// passes through untouched, to be rebuilt as <ruby> after rendering.
func preprocessFurigana(src string) string {
	return furiganaRe.ReplaceAllString(src,
		string(furiOpenSentinel)+"$1"+string(furiMidSentinel)+"$2"+string(furiCloseSentinel))
}

// postprocessFurigana swaps the sentinel markers left in rendered HTML for real
// <ruby> markup. The captured base and reading are plain text goldmark has
// already HTML-escaped, so they're safe to interpolate directly.
func postprocessFurigana(htmlOut string) string {
	return furiSentinelRe.ReplaceAllString(htmlOut, `<ruby>$1<rt>$2</rt></ruby>`)
}

// MarkdownHTML converts fact front/back text to sanitized HTML for display.
// Registered as the `md` template function.
func MarkdownHTML(src string) template.HTML {
	src = preprocessColor(src)
	src = preprocessFurigana(src)
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		slog.Error("markdown render failed, falling back to escaped text", "err", err)
		return template.HTML(template.HTMLEscapeString(src))
	}
	out := postprocessColor(buf.String())
	out = postprocessFurigana(out)
	return template.HTML(out)
}
