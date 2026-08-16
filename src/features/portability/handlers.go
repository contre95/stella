package portability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/features/cards"
	"github.com/gofiber/fiber/v2"
)

// Handler is the HTTP transport layer for import and export.
type Handler struct {
	service *Service
	cards   *cards.Handler
}

// NewHandler constructs the portability handler.
func NewHandler(service *Service, cardsHandler *cards.Handler) *Handler {
	return &Handler{service: service, cards: cardsHandler}
}

// ExportDeck returns a zip download (deck.json plus media files) for the deck.
func (h *Handler) ExportDeck(c *fiber.Ctx) error {
	exp, err := h.service.ExportDeck(c.Context(), auth.UserID(c), c.Params("id"))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := writeDeckZip(&buf, exp); err != nil {
		return err
	}
	filename := fmt.Sprintf("%s.zip", strings.ReplaceAll(exp.Name, " ", "_"))
	c.Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Set("Content-Type", "application/zip")
	return c.Send(buf.Bytes())
}

// ExportGroup returns a zip download containing all decks in the group.
func (h *Handler) ExportGroup(c *fiber.Ctx) error {
	exp, err := h.service.ExportGroup(c.Context(), auth.UserID(c), c.Params("id"))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := writeGroupZip(&buf, exp); err != nil {
		return err
	}
	filename := fmt.Sprintf("%s.zip", strings.ReplaceAll(exp.Name, " ", "_"))
	c.Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Set("Content-Type", "application/zip")
	return c.Send(buf.Bytes())
}

// ImportGroup reads an uploaded group export zip and creates a new group with
// all its decks.
func (h *Handler) ImportGroup(c *fiber.Ctx) error {
	file, err := c.FormFile("file")
	if err != nil || file == nil {
		return fiber.NewError(fiber.StatusBadRequest, "group file (.zip) is required")
	}
	f, err := file.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	exp, err := readGroupZip(f, file.Size)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if exp.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "group name is missing from the export")
	}
	if _, _, err := h.service.ImportGroup(c.Context(), auth.UserID(c), exp, ""); err != nil {
		return err
	}
	return h.cards.DeckList(c)
}

// Import auto-detects whether the uploaded file is a deck or group export and
// dispatches to the appropriate import path. Zip files with a group.json root
// entry are treated as group exports; everything else is treated as a deck.
func (h *Handler) Import(c *fiber.Ctx) error {
	file, err := c.FormFile("file")
	if err != nil || file == nil {
		return fiber.NewError(fiber.StatusBadRequest, "file (.zip or .json) is required")
	}
	f, err := file.Open()
	if err != nil {
		return err
	}
	defer f.Close()

	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	isZip := n == len(magic) && bytes.Equal(magic[:], []byte("PK\x03\x04"))
	if !isZip {
		exp := new(DeckExport)
		if err := json.NewDecoder(f).Decode(exp); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid deck file: "+err.Error())
		}
		if exp.Name == "" {
			return fiber.NewError(fiber.StatusBadRequest, "deck name is missing from the export")
		}
		nameOverride := strings.TrimSpace(c.FormValue("name"))
		if _, _, err := h.service.ImportDeck(c.Context(), auth.UserID(c), exp, nameOverride); err != nil {
			return err
		}
		return h.cards.DeckList(c)
	}

	isGroup, err := zipHasEntry(f, file.Size, groupManifestName)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid zip: "+err.Error())
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	if isGroup {
		exp, err := readGroupZip(f, file.Size)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if exp.Name == "" {
			return fiber.NewError(fiber.StatusBadRequest, "group name is missing from the export")
		}
		if _, _, err := h.service.ImportGroup(c.Context(), auth.UserID(c), exp, ""); err != nil {
			return err
		}
	} else {
		exp, err := readDeckZip(f, file.Size)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if exp.Name == "" {
			return fiber.NewError(fiber.StatusBadRequest, "deck name is missing from the export")
		}
		nameOverride := strings.TrimSpace(c.FormValue("name"))
		if _, _, err := h.service.ImportDeck(c.Context(), auth.UserID(c), exp, nameOverride); err != nil {
			return err
		}
	}
	return h.cards.DeckList(c)
}

// ImportDeck reads an uploaded deck export and creates a new deck from it.
// Zip archives (deck.json + media/) are the current format; bare JSON files
// with embedded media bytes are still accepted for old exports.
func (h *Handler) ImportDeck(c *fiber.Ctx) error {
	file, err := c.FormFile("file")
	if err != nil || file == nil {
		return fiber.NewError(fiber.StatusBadRequest, "deck file (.zip or .json) is required")
	}
	f, err := file.Open()
	if err != nil {
		return err
	}
	defer f.Close()

	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var exp *DeckExport
	if n == len(magic) && bytes.Equal(magic[:], []byte("PK\x03\x04")) {
		exp, err = readDeckZip(f, file.Size)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
	} else {
		exp = new(DeckExport)
		if err := json.NewDecoder(f).Decode(exp); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid deck file: "+err.Error())
		}
	}
	if exp.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "deck name is missing from the export")
	}
	nameOverride := strings.TrimSpace(c.FormValue("name"))
	if _, _, err := h.service.ImportDeck(c.Context(), auth.UserID(c), exp, nameOverride); err != nil {
		return err
	}
	return h.cards.DeckList(c)
}
