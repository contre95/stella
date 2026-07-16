package devicesync

import (
	"bytes"
	"errors"
	"log/slog"
	"strconv"
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
	loc     *time.Location
}

// NewHandler constructs the /api/x4 handler. loc is the instance timezone;
// hello reports its current UTC offset so devices can show local time.
func NewHandler(svc *Service, authSvc *auth.Service, loc *time.Location) *Handler {
	if loc == nil {
		loc = time.Local
	}
	return &Handler{svc: svc, authSvc: authSvc, loc: loc}
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

// hello reports the current time plus the instance timezone's UTC offset in
// seconds (DST-aware, so it flips between CET/CEST automatically). The device
// keeps a timezone-less wall clock, so it adopts now + tzOffset.
func (h *Handler) hello(c *fiber.Ctx) error {
	now := time.Now()
	_, offset := now.In(h.loc).Zone()
	return c.JSON(fiber.Map{"now": now.Unix(), "tzOffset": offset})
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
	slog.Info("reviews ingested", "device", devID, "received", received, "accepted", accepted)
	return c.JSON(fiber.Map{"accepted": accepted, "received": received})
}

// statePush merges one small batch of device state records. Devices on weak
// WiFi links upload in ~1 KB batches because one multi-KB POST needs a burst
// of full-size frames that a marginal uplink cannot deliver; each batch is
// idempotent (per-card last-writer-wins), so a failed one is simply retried.
func (h *Handler) statePush(c *fiber.Ctx) error {
	devID, _ := device(c)
	deckID := c.Params("id")
	applied, received, err := h.svc.ApplyState(c.Context(), userID(c), deckID, bytes.NewReader(c.Body()))
	if errors.Is(err, srs.ErrNotFound) {
		return fiber.ErrNotFound
	}
	if err != nil {
		slog.Error("state push failed", "deck", deckID, "device", devID, "err", err)
		return err
	}
	slog.Info("state push", "deck", deckID, "device", devID, "received", received, "applied", applied)
	return c.JSON(fiber.Map{"applied": applied, "received": received})
}

// stateGet streams the deck's scheduling state (reviewed cards only); the
// download half of the batched exchange. ?since=<epoch> narrows it to cards
// graded at or after that time; absent or 0 streams everything.
func (h *Handler) stateGet(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "application/x-ndjson")
	devID, _ := device(c)
	deckID := c.Params("id")
	since, err := strconv.ParseInt(c.Query("since", "0"), 10, 64)
	if err != nil || since < 0 {
		since = 0
	}
	err = h.svc.WriteState(c.Context(), userID(c), deckID, since, c.Response().BodyWriter())
	if errors.Is(err, srs.ErrNotFound) {
		return fiber.ErrNotFound
	}
	if err != nil {
		slog.Error("state download failed", "deck", deckID, "device", devID, "err", err)
	}
	return err
}

func (h *Handler) state(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "application/x-ndjson")
	devID, _ := device(c)
	deckID := c.Params("id")
	slog.Info("state exchange start", "deck", deckID, "device", devID, "body_bytes", len(c.Body()))
	err := h.svc.ExchangeState(c.Context(), userID(c), deckID,
		bytes.NewReader(c.Body()), c.Response().BodyWriter())
	if errors.Is(err, srs.ErrNotFound) {
		slog.Error("state exchange: deck not found", "deck", deckID, "device", devID)
		return fiber.ErrNotFound
	}
	if err != nil {
		slog.Error("state exchange failed", "deck", deckID, "device", devID, "err", err)
	}
	return err
}
