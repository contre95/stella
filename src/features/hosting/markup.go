package hosting

import (
	"bytes"
	"encoding/base64"
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

// ttsOpenRe matches a read-aloud span opener, e.g. "{tts:ja}". The close
// token is the literal "{/tts}", not "{/}": reusing "{/}" would collide with
// the color-close token, since preprocessColor globally replaces every
// "{/}" before any tts-specific pass runs. "{/tts}" doesn't contain "{/}" as
// a contiguous substring, and (like colorOpenRe/furiganaRe) it's lexically
// disjoint from the other two constructs, so preprocessing order never
// matters.
var ttsOpenRe = regexp.MustCompile(`\{tts:(en|ja)\}`)

const ttsCloseTok = "{/tts}"

// mathDisplayRe matches "$$...$$" (block formulas); mathInlineRe matches
// "$...$" (inline formulas, no "$" or newline inside so it can't span a
// paragraph or swallow an unrelated later "$"). Display is extracted first
// so its own "$$" pairs are gone before the inline pattern ever runs.
var mathDisplayRe = regexp.MustCompile(`(?s)\$\$(.+?)\$\$`)
var mathInlineRe = regexp.MustCompile(`\$([^$\n]+?)\$`)

// Sentinels are Unicode Private-Use-Area characters: inert to goldmark's
// inline parser (they're just ordinary text), so they survive markdown
// parsing (including inside bold/italic) undisturbed and get swapped for
// real markup only after goldmark has rendered the HTML.
const (
	colorOpenSentinel   = '\uE000'
	colorCloseSentinel  = '\uE001'
	furiOpenSentinel    = '\uE002'
	furiMidSentinel     = '\uE003'
	furiCloseSentinel   = '\uE004'
	ttsOpenSentinel     = '\uE005'
	ttsCloseSentinel    = '\uE006'
	mathInlineSentinel  = '\uE007'
	mathDisplaySentinel = '\uE008'
)

var colorOpenSentinelRe = regexp.MustCompile(string(colorOpenSentinel) + `([0-9a-fA-F]{6})`)

var furiSentinelRe = regexp.MustCompile(
	string(furiOpenSentinel) + `(.*?)` + string(furiMidSentinel) + `(.*?)` + string(furiCloseSentinel))

var ttsOpenSentinelRe = regexp.MustCompile(string(ttsOpenSentinel) + `(en|ja)`)

// mathInlineSentinelRe / mathDisplaySentinelRe match a formula sentinel
// wrapping its base64-encoded TeX. Base64's alphabet (letters, digits,
// "+", "/", "=") contains none of goldmark's inline-markup characters, so
// the payload is inert to markdown parsing -- and never collides with the
// sentinel itself, so the same PUA character can serve as both the open
// and close marker.
var mathInlineSentinelRe = regexp.MustCompile(string(mathInlineSentinel) + `([A-Za-z0-9+/=]*)` + string(mathInlineSentinel))
var mathDisplaySentinelRe = regexp.MustCompile(string(mathDisplaySentinel) + `([A-Za-z0-9+/=]*)` + string(mathDisplaySentinel))

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

// preprocessTTS swaps "{tts:lang}...{/tts}" for sentinel markers goldmark
// will pass through untouched.
func preprocessTTS(src string) string {
	src = ttsOpenRe.ReplaceAllString(src, string(ttsOpenSentinel)+"$1")
	return strings.ReplaceAll(src, ttsCloseTok, string(ttsCloseSentinel))
}

// ttsButtonHTML is the read-aloud button rendered at the start of every
// tts span. The JS handler (see scripts.html) reads the wrapper's
// data-tts-lang and the following .tts-text's content to drive
// speechSynthesis; toggling between the two icons on click mirrors the
// .audio-chip play/pause pattern.
const ttsButtonHTML = `<button type="button" class="tts-btn" aria-label="Read aloud">` +
	`<svg class="tts-icon-speak" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">` +
	`<path d="M3 9v6h4l5 5V4L7 9H3z"/>` +
	`<path d="M14 3.23v2.06c2.89.86 5 3.54 5 6.71s-2.11 5.85-5 6.71v2.06c4.01-.91 7-4.49 7-8.77s-2.99-7.86-7-8.77z"/>` +
	`</svg>` +
	`<svg class="tts-icon-stop" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">` +
	`<path d="M6 6h12v12H6z"/>` +
	`</svg>` +
	`</button><span class="tts-text">`

// postprocessTTS swaps the sentinel markers left in rendered HTML for a
// speaker button plus the spoken text span. The captured lang is
// constrained to "en"|"ja" by ttsOpenSentinelRe, so it's safe to
// interpolate directly.
func postprocessTTS(htmlOut string) string {
	htmlOut = ttsOpenSentinelRe.ReplaceAllString(htmlOut,
		`<span class="tts-wrap" data-tts-lang="$1">`+ttsButtonHTML)
	return strings.ReplaceAll(htmlOut, string(ttsCloseSentinel), "</span></span>")
}

// preprocessMath swaps "$$...$$" and "$...$" for sentinels wrapping their
// base64-encoded TeX, so goldmark can never reinterpret "_", "*", "^" or "\"
// inside a formula as markdown. It runs before the other preprocessors so
// braces inside a formula (e.g. "$x_{i}$") can't be mistaken for their
// "{...}"-based syntax.
func preprocessMath(src string) string {
	src = mathDisplayRe.ReplaceAllStringFunc(src, func(m string) string {
		tex := m[2 : len(m)-2]
		return string(mathDisplaySentinel) + base64.StdEncoding.EncodeToString([]byte(tex)) + string(mathDisplaySentinel)
	})
	return mathInlineRe.ReplaceAllStringFunc(src, func(m string) string {
		tex := m[1 : len(m)-1]
		return string(mathInlineSentinel) + base64.StdEncoding.EncodeToString([]byte(tex)) + string(mathInlineSentinel)
	})
}

// mathSpanHTML renders a single decoded formula sentinel match as a
// <span class="katex-math" data-tex="..."> placeholder. The element's text
// content is the literal "$...$"/"$$...$$" source, a plain-text fallback for
// when the client-side KaTeX render (see scripts.html) hasn't run yet; KaTeX
// overwrites it in place once it does.
func mathSpanHTML(b64 string, display bool) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "" // sentinel payload is always our own base64 output; unreachable in practice
	}
	escaped := template.HTMLEscapeString(string(raw))
	delim, attrs := "$"+escaped+"$", ""
	if display {
		delim, attrs = "$$"+escaped+"$$", ` data-tex-display="1"`
	}
	return `<span class="katex-math"` + attrs + ` data-tex="` + escaped + `">` + delim + `</span>`
}

// postprocessMath swaps the sentinel markers left in rendered HTML for KaTeX
// placeholder spans.
func postprocessMath(htmlOut string) string {
	htmlOut = mathDisplaySentinelRe.ReplaceAllStringFunc(htmlOut, func(m string) string {
		return mathSpanHTML(mathDisplaySentinelRe.FindStringSubmatch(m)[1], true)
	})
	return mathInlineSentinelRe.ReplaceAllStringFunc(htmlOut, func(m string) string {
		return mathSpanHTML(mathInlineSentinelRe.FindStringSubmatch(m)[1], false)
	})
}

// MarkdownHTML converts fact front/back text to sanitized HTML for display.
// Registered as the `md` template function.
func MarkdownHTML(src string) template.HTML {
	src = preprocessMath(src)
	src = preprocessColor(src)
	src = preprocessFurigana(src)
	src = preprocessTTS(src)
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		slog.Error("markdown render failed, falling back to escaped text", "err", err)
		return template.HTML(template.HTMLEscapeString(src))
	}
	out := postprocessColor(buf.String())
	out = postprocessFurigana(out)
	out = postprocessTTS(out)
	out = postprocessMath(out)
	return template.HTML(out)
}
