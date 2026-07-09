package portability

import (
	"bytes"
	"testing"
)

func TestDeckZipRoundTrip(t *testing.T) {
	exp := &DeckExport{
		Name:        "Japonés",
		Description: "Vocab",
		Image:       "cover.png",
		ImageData:   []byte("cover-bytes"),
		Facts: []FactExport{
			{
				Front:          "犬",
				Back:           "dog",
				ImageFront:     "dog.jpg",
				ImageFrontData: []byte("dog-image"),
				AudioBack:      "dog.mp3",
				AudioBackData:  []byte("dog-audio"),
				Reverse:        true,
			},
			{Front: "猫", Back: "cat"},
		},
	}

	var buf bytes.Buffer
	if err := writeDeckZip(&buf, exp); err != nil {
		t.Fatalf("writeDeckZip: %v", err)
	}
	got, err := readDeckZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("readDeckZip: %v", err)
	}

	if got.Name != exp.Name || got.Description != exp.Description || got.Image != exp.Image {
		t.Errorf("deck fields = %q/%q/%q, want %q/%q/%q",
			got.Name, got.Description, got.Image, exp.Name, exp.Description, exp.Image)
	}
	if !bytes.Equal(got.ImageData, exp.ImageData) {
		t.Errorf("cover data = %q, want %q", got.ImageData, exp.ImageData)
	}
	if len(got.Facts) != len(exp.Facts) {
		t.Fatalf("got %d facts, want %d", len(got.Facts), len(exp.Facts))
	}
	f0 := got.Facts[0]
	if f0.Front != "犬" || f0.Back != "dog" || !f0.Reverse {
		t.Errorf("fact 0 fields = %+v", f0)
	}
	if !bytes.Equal(f0.ImageFrontData, []byte("dog-image")) {
		t.Errorf("fact 0 image data = %q", f0.ImageFrontData)
	}
	if !bytes.Equal(f0.AudioBackData, []byte("dog-audio")) {
		t.Errorf("fact 0 audio data = %q", f0.AudioBackData)
	}
	f1 := got.Facts[1]
	if f1.Front != "猫" || f1.Back != "cat" || f1.ImageFrontData != nil || f1.AudioBackData != nil {
		t.Errorf("fact 1 fields = %+v", f1)
	}
}

func TestReadDeckZipRejectsMissingManifest(t *testing.T) {
	empty := []byte("PK\x05\x06" + string(make([]byte, 18)))
	if _, err := readDeckZip(bytes.NewReader(empty), int64(len(empty))); err == nil {
		t.Error("expected error for zip without deck.json")
	}
}
