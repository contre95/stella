package opds

import (
	"fmt"

	"github.com/contre95/stella/src/features/auth"
	"github.com/gofiber/fiber/v2"
)

// Handler is the HTTP transport layer for the OPDS catalog.
type Handler struct {
	service *Service
}

// NewHandler constructs the OPDS handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Catalog serves the root OPDS feed listing all groups and decks.
func (h *Handler) Catalog(c *fiber.Ctx) error {
	baseURL := h.baseURL(c)
	feed, err := h.service.CatalogFeed(c.Context(), auth.UserID(c), baseURL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}
	return h.sendFeed(c, feed)
}

// Group serves an OPDS feed for a single group.
func (h *Handler) Group(c *fiber.Ctx) error {
	baseURL := h.baseURL(c)
	feed, err := h.service.GroupFeed(c.Context(), auth.UserID(c), c.Params("id"), baseURL)
	if err != nil {
		return c.Status(fiber.StatusNotFound).SendString("group not found")
	}
	return h.sendFeed(c, feed)
}

// sendFeed serialises the feed as XML and sends it with the OPDS content type.
func (h *Handler) sendFeed(c *fiber.Ctx, feed *Feed) error {
	c.Set("Content-Type", "application/atom+xml;profile=opds-catalog; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=300")
	return c.XML(feed)
}

// baseURL derives the public base URL from the request. It checks
// X-Forwarded-Host / X-Forwarded-Proto first (reverse-proxy friendly), then
// falls back to the request's own Host header and scheme.
func (h *Handler) baseURL(c *fiber.Ctx) string {
	if proto := c.Get("X-Forwarded-Proto"); proto != "" {
		if host := c.Get("X-Forwarded-Host"); host != "" {
			return fmt.Sprintf("%s://%s", proto, host)
		}
	}
	scheme := "http"
	if csecure := c.Get("X-Forwarded-Scheme"); csecure != "" {
		scheme = csecure
	} else if c.Context().IsTLS() {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, c.Hostname())
}
