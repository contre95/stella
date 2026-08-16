package cards

import (
	"github.com/contre95/stella/src/features/hosting/respond"
	"github.com/gofiber/fiber/v2"
)

// addFactRequest is the text-field payload for POST /cards and
// POST /decks/:id/cards. File fields (image_front/back, audio_front/back)
// are handled separately via c.FormFile — see Handler.saveImage/saveAudio.
type addFactRequest struct {
	Front   string `form:"front" field:"front" validate:"required"`
	Back    string `form:"back" field:"back"`
	DeckID  string `form:"deckId" field:"deckId" validate:"required"`
	Reverse bool   `form:"reverse" field:"reverse"`
	Grade   string `form:"grade" field:"grade"`
}

// editFactRequest is the text-field payload for PUT /cards/:id.
type editFactRequest struct {
	Front   string `form:"front" field:"front" validate:"required"`
	Back    string `form:"back" field:"back"`
	Reverse bool   `form:"reverse" field:"reverse"`
}

// nameRequest is the payload for the single-name forms with no length cap:
// create/rename group, add a deck tag.
type nameRequest struct {
	Name string `form:"name" field:"name" validate:"required"`
}

// deckNameRequest is the payload for a deck's name field, capped at 32
// characters (decks previously silently truncated instead of rejecting —
// rejecting with an inline error is clearer than mangling the input).
type deckNameRequest struct {
	Name string `form:"name" field:"name" validate:"required,max=32"`
}

// addDeckRequest is the text-field payload for POST /decks. The cover image
// is handled separately via c.FormFile — see Handler.saveImage.
type addDeckRequest struct {
	Name        string `form:"name" field:"name" validate:"required,max=32"`
	Description string `form:"description" field:"description"`
	GroupID     string `form:"group_id" field:"group_id"`
}

// mergeFileErr folds a saveImage/saveAudio error into errs under key, when
// err is the "wrong file type" validation error (a 400 fiber.Error) rather
// than an infra failure. Infra failures (disk I/O, permissions) are returned
// as-is by the caller and become a 500 toast, same as any other internal
// error — only user-fixable input problems become inline field errors.
func mergeFileErr(errs respond.FieldErrors, key string, err error) (respond.FieldErrors, error) {
	if err == nil {
		return errs, nil
	}
	if fe, ok := err.(*fiber.Error); ok && fe.Code == fiber.StatusBadRequest {
		if errs == nil {
			errs = respond.FieldErrors{}
		}
		errs[key] = fe.Message
		return errs, nil
	}
	return errs, err
}

// firstFieldError picks one message out of a FieldErrors map for contexts
// that still only support a single toast message (map iteration order is
// unspecified, but these maps hold at most one or two entries in practice).
func firstFieldError(errs respond.FieldErrors) string {
	for _, msg := range errs {
		return msg
	}
	return "Invalid input"
}
