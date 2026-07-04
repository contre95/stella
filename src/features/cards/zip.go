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
// A group export zip contains groupManifestName at the root and one numbered
// deck sub-directory per deck, each following the same deck zip layout.
const (
	deckManifestName  = "deck.json"
	groupManifestName = "group.json"
	mediaDir          = "media"
	decksDir          = "decks"
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

// writeGroupZip encodes a GroupExport as a zip archive. The layout is:
//
//	group.json          – {"name": "…"}
//	decks/0/deck.json   – DeckExport manifest for the first deck
//	decks/0/media/*     – media files for the first deck
//	decks/1/deck.json   – second deck, etc.
func writeGroupZip(w io.Writer, exp *GroupExport) error {
	zw := zip.NewWriter(w)
	type groupMeta struct {
		Name string `json:"name"`
	}
	gf, err := zw.Create(groupManifestName)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(gf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(groupMeta{Name: exp.Name}); err != nil {
		return err
	}
	for i := range exp.Decks {
		prefix := fmt.Sprintf("%s/%d/", decksDir, i)
		de := &exp.Decks[i]
		written := map[string]bool{}
		addMedia := func(name string, data []byte) error {
			if name == "" || len(data) == 0 || written[name] {
				return nil
			}
			written[name] = true
			f, err := zw.Create(prefix + mediaDir + "/" + name)
			if err != nil {
				return err
			}
			_, err = f.Write(data)
			return err
		}
		if err := addMedia(de.Image, de.ImageData); err != nil {
			return err
		}
		for j := range de.Facts {
			fe := &de.Facts[j]
			for _, m := range []struct{ name string; data []byte }{
				{fe.ImageFront, fe.ImageFrontData},
				{fe.ImageBack, fe.ImageBackData},
				{fe.AudioFront, fe.AudioFrontData},
				{fe.AudioBack, fe.AudioBackData},
			} {
				if err := addMedia(m.name, m.data); err != nil {
					return err
				}
			}
		}
		manifest := *de
		manifest.ImageData = nil
		manifest.Facts = make([]FactExport, len(de.Facts))
		for j, fe := range de.Facts {
			fe.ImageFrontData, fe.ImageBackData = nil, nil
			fe.AudioFrontData, fe.AudioBackData = nil, nil
			manifest.Facts[j] = fe
		}
		mf, err := zw.Create(prefix + deckManifestName)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(mf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(&manifest); err != nil {
			return err
		}
	}
	return zw.Close()
}

// readGroupZip parses a group export zip back into a GroupExport with all
// media bytes reattached.
func readGroupZip(r io.ReaderAt, size int64) (*GroupExport, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("invalid zip: %w", err)
	}
	type groupMeta struct {
		Name string `json:"name"`
	}
	var meta *groupMeta
	// deckIdx → manifest
	manifests := map[string]*DeckExport{}
	// deckIdx/filename → bytes
	mediaBytes := map[string]map[string][]byte{}

	for _, zf := range zr.File {
		switch {
		case zf.Name == groupManifestName:
			rc, err := zf.Open()
			if err != nil {
				return nil, err
			}
			meta = new(groupMeta)
			err = json.NewDecoder(rc).Decode(meta)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("invalid %s: %w", groupManifestName, err)
			}
		case strings.HasPrefix(zf.Name, decksDir+"/") && !zf.FileInfo().IsDir():
			// Path: decks/<idx>/deck.json  or  decks/<idx>/media/<file>
			rest := strings.TrimPrefix(zf.Name, decksDir+"/")
			parts := strings.SplitN(rest, "/", 3)
			if len(parts) < 2 {
				continue
			}
			idx := parts[0]
			switch {
			case parts[1] == deckManifestName && len(parts) == 2:
				rc, err := zf.Open()
				if err != nil {
					return nil, err
				}
				de := new(DeckExport)
				err = json.NewDecoder(rc).Decode(de)
				rc.Close()
				if err != nil {
					return nil, fmt.Errorf("invalid deck manifest at index %s: %w", idx, err)
				}
				manifests[idx] = de
			case parts[1] == mediaDir && len(parts) == 3 && !zf.FileInfo().IsDir():
				rc, err := zf.Open()
				if err != nil {
					return nil, err
				}
				data, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, err
				}
				if mediaBytes[idx] == nil {
					mediaBytes[idx] = map[string][]byte{}
				}
				mediaBytes[idx][path.Base(parts[2])] = data
			}
		}
	}
	if meta == nil {
		return nil, fmt.Errorf("%s not found in zip", groupManifestName)
	}
	exp := &GroupExport{Name: meta.Name, Decks: make([]DeckExport, len(manifests))}
	for idx, de := range manifests {
		media := mediaBytes[idx]
		de.ImageData = media[de.Image]
		for i := range de.Facts {
			fe := &de.Facts[i]
			fe.ImageFrontData = media[fe.ImageFront]
			fe.ImageBackData = media[fe.ImageBack]
			fe.AudioFrontData = media[fe.AudioFront]
			fe.AudioBackData = media[fe.AudioBack]
		}
		// idx is a numeric string; parse it to place decks in order.
		n := 0
		fmt.Sscanf(idx, "%d", &n)
		if n < len(exp.Decks) {
			exp.Decks[n] = *de
		}
	}
	return exp, nil
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
