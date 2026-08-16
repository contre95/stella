package devicesync

import (
	"strings"

	"github.com/contre95/stella/src/markup"
)

// Normalizer curates one face's text for a specific client's rendering
// capability before it goes out over the wire. hasImage reports whether this
// face has an attached image — image files are never sent through the sync
// protocols, so only clients that can't display them at all need a textual
// stand-in.
type Normalizer func(text string, hasImage bool) string

// X4Normalizer renders content for the x4 e-ink device: bare text only, with
// markdown, color, furigana, tts and center markup all resolved to their
// visible text and no leftover syntax — the device renders literal
// characters and has no markup support at all. Images can't be displayed
// either, so their presence is called out as a trailing "[IMAGE]" line.
func X4Normalizer(text string, hasImage bool) string {
	out := markup.PlainText(text)
	if hasImage {
		out = strings.TrimSpace(out + "\n[IMAGE]")
	}
	return out
}
