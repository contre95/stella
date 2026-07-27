package devicesync

import "testing"

func TestCurateForDeviceStripsFormatting(t *testing.T) {
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
		{"list", "- one\n- two", "one\ntwo"},
	} {
		got := curateForDevice(tc.in)
		if got != tc.want {
			t.Errorf("%s: curateForDevice(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
