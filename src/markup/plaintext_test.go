package markup

import "testing"

func TestPlainTextStripsFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"heading", "# Kanji", "Kanji"},
		{"bold", "**very** important", "very important"},
		{"color span", "{#ff6600}orange{/}", "orange"},
		{"color wrapping bold", "{#ff6600}**bold**{/}", "bold"},
		{"unclosed color span", "{#ff6600}orange", "orange"},
		{"stray closer", "plain{/}text", "plaintext"},
		{"furigana to parens", "{漢字|かんじ}", "漢字（かんじ）"},
		{"furigana in sentence", "これは{漢字|かんじ}です", "これは漢字（かんじ）です"},
		{"furigana per character", "{漢|かん}{字|じ}", "漢（かん）字（じ）"},
		{"color inside furigana reading", "{漢字|{#ff0000}かんじ{/}}", "漢字（かんじ）"},
		{"color inside furigana base", "{{#ff0000}漢字{/}|かんじ}", "漢字（かんじ）"},
		{"plain text unchanged", "just a question", "just a question"},
		{"bullet list", "- one\n- two", "- one\n- two"},
		{"numbered list", "1. one\n2. two", "1. one\n2. two"},
		{"nested list", "- outer\n  - inner", "- outer\n- inner"},
		{"tts span unwraps", "{tts:en}hello{/tts}", "hello"},
		{"tts span japanese", "{tts:ja}こんにちは{/tts}", "こんにちは"},
		{"unclosed tts span", "{tts:en}hello", "hello"},
		{"tts wrapping furigana", "{tts:ja}{漢字|かんじ}{/tts}", "漢字（かんじ）"},
		{"tts wrapping color", "{tts:en}{#ff6600}orange{/}{/tts}", "orange"},
		{"center directive unwraps", "{center}short answer{/center}", "short answer"},
		{"center wrapping formatting", "{center}**bold**{/center}", "bold"},
		{"text around center stays", "before {center}mid{/center} after", "before mid after"},
		{"unclosed center", "{center}no close", "no close"},
		{"inline math strips delimiters", "the value is $x^2$", "the value is x^2"},
		{"display math strips delimiters", "$$x^2 + y^2 = z^2$$", "x^2 + y^2 = z^2"},
		{"math with braces", "$x_{i}$ is a term", "x_{i} is a term"},
	} {
		got := PlainText(tc.in)
		if got != tc.want {
			t.Errorf("%s: PlainText(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
