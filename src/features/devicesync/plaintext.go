package devicesync

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// plainTextParser parses fact content down to an AST for stripping; the
// device never sees rendered HTML, so no renderer is needed here.
var plainTextParser = goldmark.New().Parser()

// colorOpenRe matches the color span opener the web renderer supports, e.g.
// "{#ff6600}". The x4 has no color support at all, so this curation drops it
// entirely rather than trying to represent it.
var colorOpenRe = regexp.MustCompile(`\{#([0-9a-fA-F]{6})\}`)

const colorCloseTok = "{/}"

// furiganaRe matches the web renderer's ruby syntax, e.g. "{漢字|かんじ}". The
// x4 has no ruby support, so curation flattens it to "base（reading）" with
// full-width parens, keeping the reading readable inline.
var furiganaRe = regexp.MustCompile(`\{([^{}|\n]+)\|([^{}|\n]+)\}`)

// blockKinds close with a newline once fully walked, so separate paragraphs,
// headings and list items don't run into each other.
var blockKinds = map[ast.NodeKind]bool{
	ast.KindParagraph: true,
	ast.KindHeading:   true,
	ast.KindListItem:  true,
}

// curateForDevice strips all markdown formatting — including color spans —
// down to bare words. The x4 e-reader has no markdown/HTML support: it
// renders literal characters, so any markup left in would show up on-screen
// as stray symbols.
func curateForDevice(src string) string {
	// Strip colour first so a colour span nested inside a furigana reading
	// (e.g. "{漢字|{#ff0000}かんじ{/}}") is gone before the ruby is flattened —
	// otherwise its braces would stop furiganaRe from matching.
	stripped := colorOpenRe.ReplaceAllString(src, "")
	stripped = strings.ReplaceAll(stripped, colorCloseTok, "")
	stripped = furiganaRe.ReplaceAllString(stripped, "${1}（${2}）")

	source := []byte(stripped)
	doc := plainTextParser.Parse(text.NewReader(source))

	var buf strings.Builder
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			if blockKinds[n.Kind()] {
				buf.WriteByte('\n')
			}
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Text:
			buf.Write(t.Segment.Value(source))
			if t.SoftLineBreak() || t.HardLineBreak() {
				buf.WriteByte('\n')
			}
		case *ast.String:
			buf.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})

	return strings.TrimSpace(buf.String())
}
