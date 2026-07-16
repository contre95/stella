package hosting

import (
	"bytes"
	"html/template"
	"log/slog"

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

// MarkdownHTML converts fact front/back text to sanitized HTML for display.
// Registered as the `md` template function.
func MarkdownHTML(src string) template.HTML {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		slog.Error("markdown render failed, falling back to escaped text", "err", err)
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(buf.String())
}
