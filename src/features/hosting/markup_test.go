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
		{"furigana ruby", "{漢字|かんじ}", "<ruby>漢字<rt>かんじ</rt></ruby>"},
		{"furigana in sentence", "これは{漢字|かんじ}です", "<ruby>漢字<rt>かんじ</rt></ruby>"},
		{"furigana per character", "{漢|かん}{字|じ}", "<ruby>漢<rt>かん</rt></ruby><ruby>字<rt>じ</rt></ruby>"},
		{"furigana inside bold", "**{漢字|かんじ}**", "<strong><ruby>漢字<rt>かんじ</rt></ruby></strong>"},
		{"color inside furigana reading", "{漢字|{#ff0000}かんじ{/}}", `<ruby>漢字<rt><span style="color:#ff0000">かんじ</span></rt></ruby>`},
		{"color inside furigana base", "{{#ff0000}漢字{/}|かんじ}", `<ruby><span style="color:#ff0000">漢字</span><rt>かんじ</rt></ruby>`},
		{"braces without a pipe stay literal", "{not furigana}", "{not furigana}"},
		{"tts span", "{tts:en}hello{/tts}", `<span class="tts-wrap" data-tts-lang="en">`},
		{"tts span wraps text", "{tts:ja}こんにちは{/tts}", `<span class="tts-text">こんにちは</span></span>`},
		{"unclosed tts span degrades", "{tts:en}hello", `<span class="tts-text">hello`},
		{"tts wrapping furigana", "{tts:ja}{漢字|かんじ}{/tts}", `<span class="tts-text"><ruby>漢字<rt>かんじ</rt></ruby></span></span>`},
		{"tts wrapping color", "{tts:en}{#ff6600}orange{/}{/tts}", `<span class="tts-text"><span style="color:#ff6600">orange</span></span></span>`},
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
		`{<script>alert(1)</script>|x}`,
		`{base|<img src=x onerror=alert(1)>}`,
		`{tts:en}<script>alert(1)</script>{/tts}`,
	} {
		got := string(MarkdownHTML(in))
		for _, banned := range []string{"<script", "onerror", "javascript:", "<a href=\"https://evil"} {
			if strings.Contains(got, banned) {
				t.Errorf("MarkdownHTML(%q) = %q, must not contain %q", in, got, banned)
			}
		}
	}
}
