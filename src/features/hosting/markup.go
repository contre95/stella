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

// Sentinels are Unicode Private-Use-Area characters: inert to goldmark's
// inline parser (they're just ordinary text), so they survive markdown
// parsing (including inside bold/italic) undisturbed and get swapped for
// real markup only after goldmark has rendered the HTML.
const (
	colorOpenSentinel  = '\uE000'
	colorCloseSentinel = '\uE001'
)

var colorOpenSentinelRe = regexp.MustCompile(string(colorOpenSentinel) + `([0-9a-fA-F]{6})`)

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

// MarkdownHTML converts fact front/back text to sanitized HTML for display.
// Registered as the `md` template function.
func MarkdownHTML(src string) template.HTML {
	src = preprocessColor(src)
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		slog.Error("markdown render failed, falling back to escaped text", "err", err)
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(postprocessColor(buf.String()))
}
