// Package respond centralises the dual HTMX/JSON response branching so handlers
// stay thin: HTML partials for HTMX requests, JSON otherwise.
package respond

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// IsHTMX reports whether the request came from HTMX.
func IsHTMX(c *fiber.Ctx) bool {
	return c.Get("HX-Request") == "true"
}

// Section renders the full page shell on a hard load (or non-HTMX request) and
// only the section partial on an HTMX navigation. This avoids the broken-page
// problem on F5.
func Section(c *fiber.Ctx, section string, data fiber.Map) error {
	if data == nil {
		data = fiber.Map{}
	}
	data["Section"] = section
	if IsHTMX(c) {
		return c.Render("sections/"+section, data)
	}
	return c.Render("partials/main", data)
}

// Partial returns an HTML fragment for HTMX, or JSON for API clients.
func Partial(c *fiber.Ctx, template string, data fiber.Map) error {
	if IsHTMX(c) {
		return c.Render(template, data)
	}
	return c.JSON(data)
}

// ToastOk renders a success toast (HTMX) or a JSON ok payload.
func ToastOk(c *fiber.Ctx, msg string) error {
	if IsHTMX(c) {
		return c.Render("toast/toastOk", fiber.Map{"Msg": msg})
	}
	return c.JSON(fiber.Map{"ok": true, "msg": msg})
}

// ToastErr renders an error toast (HTMX) or a JSON error payload with status.
// For 5xx statuses the underlying message is logged but never sent to the
// client — those come from internal errors (DB failures, filesystem paths,
// etc.) that back-end code passes through via err.Error(), and echoing them
// back is an information leak. 4xx messages are validation text meant to be
// user-facing, so they pass through unchanged.
func ToastErr(c *fiber.Ctx, status int, msg string) error {
	c.Status(status)
	if status >= fiber.StatusInternalServerError {
		slog.Error("request failed", "path", c.Path(), "status", status, "err", msg)
		msg = "Something went wrong. Please try again."
	}
	if IsHTMX(c) {
		return c.Render("toast/toastErr", fiber.Map{"Msg": msg})
	}
	return c.JSON(fiber.Map{"ok": false, "msg": msg})
}

// ToastInfo renders an info toast (HTMX) or a JSON ok payload.
func ToastInfo(c *fiber.Ctx, msg string) error {
	if IsHTMX(c) {
		return c.Render("toast/toastInfo", fiber.Map{"Msg": msg})
	}
	return c.JSON(fiber.Map{"ok": true, "msg": msg})
}
