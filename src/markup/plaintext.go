package markup

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// plainTextParser parses fact content down to an AST for stripping; plain
// text clients never see rendered HTML, so no renderer is needed here.
var plainTextParser = markdown.Parser()

// blockKinds close with a newline once fully walked, so separate paragraphs,
// headings and list items don't run into each other.
var blockKinds = map[ast.NodeKind]bool{
	ast.KindParagraph: true,
	ast.KindHeading:   true,
	ast.KindListItem:  true,
}

// PlainText renders fact content to bare text: markdown formatting, color
// spans, furigana, tts and center wrappers are all resolved to their visible
// text with no leftover syntax. Used by clients that cannot render markup at
// all (e.g. the x4 e-ink device).
func PlainText(src string) string {
	// Strip math first: it's the only construct whose payload may itself
	// contain braces (e.g. "$x_{i}$"), which must be gone before the
	// brace-based color/furigana/tts/center patterns run over the text.
	// Delimiters are dropped but the raw TeX is kept as literal text —
	// there's no way to "render" it further for a plain-text client.
	stripped := mathDisplayRe.ReplaceAllString(src, "$1")
	stripped = mathInlineRe.ReplaceAllString(stripped, "$1")

	// Strip color next so a color span nested inside a furigana reading
	// (e.g. "{漢字|{#ff0000}かんじ{/}}") is gone before the ruby is
	// flattened — otherwise its braces would stop furiganaRe from matching.
	stripped = colorOpenRe.ReplaceAllString(stripped, "")
	stripped = strings.ReplaceAll(stripped, colorCloseTok, "")

	// tts and center are unwrapped to their inner content; both tokens are
	// lexically disjoint from color/furigana (see their definitions in
	// markup.go), so this can run in any order relative to those.
	stripped = ttsOpenRe.ReplaceAllString(stripped, "")
	stripped = strings.ReplaceAll(stripped, ttsCloseTok, "")
	stripped = strings.ReplaceAll(stripped, centerOpenTok, "")
	stripped = strings.ReplaceAll(stripped, centerCloseTok, "")

	stripped = furiganaRe.ReplaceAllString(stripped, "${1}（${2}）")

	source := []byte(stripped)
	doc := plainTextParser.Parse(text.NewReader(source))

	// listNum tracks the running item number for each nested ordered list
	// (0 for an unordered list); pushed on entering ast.List, popped on exit,
	// so a marker can be written the moment a list item starts.
	var listNum []int
	var buf strings.Builder
	// nlIfNeeded ensures the buffer ends with a newline, without ever adding
	// a second one — a tight list item's content isn't itself a blockKind
	// (see blockKinds), so a nested list starting right after it needs this
	// to avoid running its marker into the parent item's text; the same
	// idempotence stops the parent item's own closing newline (blockKinds)
	// from doubling up after that nested list already closed with one.
	nlIfNeeded := func() {
		if s := buf.String(); len(s) > 0 && s[len(s)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if n.Kind() == ast.KindList {
			if entering {
				nlIfNeeded()
				start := 0
				if l := n.(*ast.List); l.IsOrdered() {
					start = l.Start
				}
				listNum = append(listNum, start)
			} else {
				listNum = listNum[:len(listNum)-1]
			}
			return ast.WalkContinue, nil
		}
		if entering && n.Kind() == ast.KindListItem && len(listNum) > 0 {
			top := len(listNum) - 1
			if listNum[top] == 0 {
				buf.WriteString("- ")
			} else {
				fmt.Fprintf(&buf, "%d. ", listNum[top])
				listNum[top]++
			}
		}
		if !entering {
			if blockKinds[n.Kind()] {
				nlIfNeeded()
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
