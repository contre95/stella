package portability

import (
	"bytes"
	"testing"

	"github.com/contre95/stella/src/srs"
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
				Cards: []CardStats{
					{
						View: srs.ViewForward, Grade: 4, Easiness: 2.6, RetReps: 3, LastRep: 100, NextRep: 200, Active: true,
						Reviews: []ReviewStats{
							{Grade: 2, RetReps: 1, ScheduledInterval: 86400, Timestamp: 50, Source: "web"},
							{Grade: 4, RetReps: 3, ScheduledInterval: 172800, Timestamp: 100, Source: "web"},
						},
					},
					{View: srs.ViewReverse, Grade: -1, Easiness: 2.5, Active: true},
				},
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
	if len(f0.Cards) != 2 {
		t.Fatalf("fact 0 cards = %d, want 2", len(f0.Cards))
	}
	fwd := f0.Cards[0]
	if fwd.View != srs.ViewForward || fwd.Grade != 4 || fwd.Easiness != 2.6 || fwd.RetReps != 3 || fwd.NextRep != 200 {
		t.Errorf("fact 0 forward card = %+v", fwd)
	}
	if len(fwd.Reviews) != 2 || fwd.Reviews[1].Grade != 4 || fwd.Reviews[1].ScheduledInterval != 172800 {
		t.Errorf("fact 0 forward reviews = %+v", fwd.Reviews)
	}
	if f0.Cards[1].View != srs.ViewReverse || f0.Cards[1].Grade != -1 {
		t.Errorf("fact 0 reverse card = %+v", f0.Cards[1])
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
