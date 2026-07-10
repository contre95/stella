package devicesync

import (
	"bytes"
	"errors"
	"strings"
	"time"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/srs"
	"github.com/gofiber/fiber/v2"
)

// Handler exposes the sync protocol under /api/x4. Every route requires a
// bearer API token — even in single-user mode, where the token doubles as the
// only thing keeping a LAN-open write API from being anonymous. Tokens are
// minted at /settings/tokens and placed in the device's sync.json.
type Handler struct {
	svc     *Service
	authSvc *auth.Service
}

// NewHandler constructs the /api/x4 handler.
func NewHandler(svc *Service, authSvc *auth.Service) *Handler {
	return &Handler{svc: svc, authSvc: authSvc}
}

const localsSyncUserID = "SyncUserID"

func (h *Handler) bearerAuth(c *fiber.Ctx) error {
	parts := strings.Fields(c.Get(fiber.HeaderAuthorization))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return fiber.NewError(fiber.StatusUnauthorized, "missing bearer token")
	}
	user, err := h.authSvc.UserByBearer(c.Context(), parts[1])
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid token")
	}
	c.Locals(localsSyncUserID, user.ID)
	return c.Next()
}

func userID(c *fiber.Ctx) string {
	id, _ := c.Locals(localsSyncUserID).(string)
	return id
}

// device returns the device identity headers: a stable ID (efuse MAC hex,
// used in review idempotency keys) and a human name (the review source).
func device(c *fiber.Ctx) (id, name string) {
	id = c.Get("X-Device-Id")
	if id == "" {
		id = "x4"
	}
	name = c.Get("X-Device-Name")
	if name == "" {
		name = "x4"
	}
	return id, name
}

func (h *Handler) hello(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"now": time.Now().Unix()})
}

func (h *Handler) manifest(c *fiber.Ctx) error {
	decks, err := h.svc.Manifest(c.Context(), userID(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"decks": decks})
}

func (h *Handler) deckCSV(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
	err := h.svc.WriteDeckCSV(c.Context(), userID(c), c.Params("id"), c.Response().BodyWriter())
	if errors.Is(err, srs.ErrNotFound) {
		return fiber.ErrNotFound
	}
	return err
}

func (h *Handler) reviews(c *fiber.Ctx) error {
	devID, devName := device(c)
	accepted, received, err := h.svc.IngestReviews(c.Context(), userID(c), devID, devName, bytes.NewReader(c.Body()))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return c.JSON(fiber.Map{"accepted": accepted, "received": received})
}

func (h *Handler) state(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "application/x-ndjson")
	err := h.svc.ExchangeState(c.Context(), userID(c), c.Params("id"),
		bytes.NewReader(c.Body()), c.Response().BodyWriter())
	if errors.Is(err, srs.ErrNotFound) {
		return fiber.ErrNotFound
	}
	return err
}
