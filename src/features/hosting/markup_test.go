package hosting

import (
	"strings"
	"testing"
)

func TestMarkdownHTMLRendersFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"heading", "# Kanji", "<h1>Kanji</h1>"},
		{"bold", "**very** important", "<strong>very</strong>"},
		{"list", "- one\n- two", "<li>one</li>"},
		{"plain text unchanged", "just a question", "<p>just a question</p>"},
		{"color span", "{#ff6600}orange{/}", `<span style="color:#ff6600">orange</span>`},
		{"color wrapping bold", "{#ff6600}**bold**{/}", `<span style="color:#ff6600"><strong>bold</strong></span>`},
		{"unclosed color span degrades", "{#ff6600}orange", `<span style="color:#ff6600">orange`},
	} {
		got := string(MarkdownHTML(tc.in))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: MarkdownHTML(%q) = %q, want it to contain %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestMarkdownHTMLHardWrapsNewlines(t *testing.T) {
	got := string(MarkdownHTML("line one\nline two"))
	if !strings.Contains(got, "<br") {
		t.Errorf("single newline should render as <br>, got %q", got)
	}
}

// Fact content is arbitrary user/import data; it must never reach the page as
// executable markup.
func TestMarkdownHTMLNeverEmitsRawHTML(t *testing.T) {
	for _, in := range []string{
		`<script>alert(1)</script>`,
		`inline <img src=x onerror=alert(1)> html`,
		`[click](javascript:alert(1))`,
		`![x](javascript:alert(1))`,
		`<a href="https://evil.example">x</a>`,
	} {
		got := string(MarkdownHTML(in))
		for _, banned := range []string{"<script", "onerror", "javascript:", "<a href=\"https://evil"} {
			if strings.Contains(got, banned) {
				t.Errorf("MarkdownHTML(%q) = %q, must not contain %q", in, got, banned)
			}
		}
	}
}
