package cards

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

// A deck export zip contains deckManifestName at the root plus the referenced
// media files under mediaDir/, named exactly as the manifest references them.
const (
	deckManifestName = "deck.json"
	mediaDir         = "media"
)

// writeDeckZip encodes exp as a zip archive: media bytes become files under
// mediaDir/ and the manifest is written without them, since the zip entries
// are the canonical copies.
func writeDeckZip(w io.Writer, exp *DeckExport) error {
	zw := zip.NewWriter(w)
	written := map[string]bool{}
	add := func(name string, data []byte) error {
		if name == "" || len(data) == 0 || written[name] {
			return nil
		}
		written[name] = true
		f, err := zw.Create(mediaDir + "/" + name)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	if err := add(exp.Image, exp.ImageData); err != nil {
		return err
	}
	for i := range exp.Facts {
		fe := &exp.Facts[i]
		files := []struct {
			name string
			data []byte
		}{
			{fe.ImageFront, fe.ImageFrontData},
			{fe.ImageBack, fe.ImageBackData},
			{fe.AudioFront, fe.AudioFrontData},
			{fe.AudioBack, fe.AudioBackData},
		}
		for _, m := range files {
			if err := add(m.name, m.data); err != nil {
				return err
			}
		}
	}

	manifest := *exp
	manifest.ImageData = nil
	manifest.Facts = make([]FactExport, len(exp.Facts))
	for i, fe := range exp.Facts {
		fe.ImageFrontData, fe.ImageBackData = nil, nil
		fe.AudioFrontData, fe.AudioBackData = nil, nil
		manifest.Facts[i] = fe
	}
	mf, err := zw.Create(deckManifestName)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&manifest); err != nil {
		return err
	}
	return zw.Close()
}

// readDeckZip parses a deck export zip back into a DeckExport with the media
// bytes reattached from the mediaDir/ entries.
func readDeckZip(r io.ReaderAt, size int64) (*DeckExport, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("invalid zip: %w", err)
	}
	var exp *DeckExport
	media := map[string][]byte{}
	for _, zf := range zr.File {
		switch {
		case zf.Name == deckManifestName:
			rc, err := zf.Open()
			if err != nil {
				return nil, err
			}
			exp = new(DeckExport)
			err = json.NewDecoder(rc).Decode(exp)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("invalid %s: %w", deckManifestName, err)
			}
		case strings.HasPrefix(zf.Name, mediaDir+"/") && !zf.FileInfo().IsDir():
			rc, err := zf.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			// Base defuses any path segments a hand-crafted zip might carry;
			// the manifest only ever references bare filenames.
			media[path.Base(zf.Name)] = data
		}
	}
	if exp == nil {
		return nil, fmt.Errorf("%s not found in zip", deckManifestName)
	}
	exp.ImageData = media[exp.Image]
	for i := range exp.Facts {
		fe := &exp.Facts[i]
		fe.ImageFrontData = media[fe.ImageFront]
		fe.ImageBackData = media[fe.ImageBack]
		fe.AudioFrontData = media[fe.AudioFront]
		fe.AudioBackData = media[fe.AudioBack]
	}
	return exp, nil
}
