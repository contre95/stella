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
		{"plain text unchanged", "just a question", "just a question"},
		{"list", "- one\n- two", "one\ntwo"},
	} {
		got := curateForDevice(tc.in)
		if got != tc.want {
			t.Errorf("%s: curateForDevice(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
