// Package respond centralises the dual HTMX/JSON response branching so handlers
// stay thin: HTML partials for HTMX requests, JSON otherwise.
package respond

import (
	"errors"
	"log/slog"
	"reflect"
	"strings"

	"github.com/contre95/stella/src/srs"
	"github.com/go-playground/validator/v10"
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

// NotFound translates a service-layer srs.ErrNotFound into a 404 fiber.Error
// carrying msg; any other error (or nil) passes through unchanged. The
// caller picks msg, so the handler — not this helper — decides what the
// client sees for each specific lookup.
func NotFound(err error, msg string) error {
	if errors.Is(err, srs.ErrNotFound) {
		return fiber.NewError(fiber.StatusNotFound, msg)
	}
	return err
}

// ToastInfo renders an info toast (HTMX) or a JSON ok payload.
func ToastInfo(c *fiber.Ctx, msg string) error {
	if IsHTMX(c) {
		return c.Render("toast/toastInfo", fiber.Map{"Msg": msg})
	}
	return c.JSON(fiber.Map{"ok": true, "msg": msg})
}

// FieldErrors maps a form field's `name=` attribute to its user-facing
// validation message, for forms that show errors inline next to the field
// rather than as a toast.
type FieldErrors map[string]string

// validate is a dedicated *validator.Validate for HTTP form validation,
// separate from config's — it needs field-name resolution tied to HTML
// `name=` attributes, which config's struct validation has no use for.
var validate = func() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		if name := f.Tag.Get("field"); name != "" {
			return name
		}
		return strings.ToLower(f.Name)
	})
	return v
}()

// Validate runs struct-tag validation (see validator/v10) and returns nil on
// success, or a FieldErrors map keyed by each field's `field:"..."` tag (or
// its lowercased Go name, absent that tag) with one message per field — the
// first failing rule wins, which is enough for a single-line inline error.
func Validate(s any) FieldErrors {
	err := validate.Struct(s)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return nil
	}
	errs := FieldErrors{}
	for _, fe := range verrs {
		key := fe.Field()
		if _, exists := errs[key]; exists {
			continue
		}
		errs[key] = fieldMessage(fe)
	}
	return errs
}

// fieldMessage renders one human-readable sentence per validator tag
// actually used in this codebase — extend as new tags get adopted.
func fieldMessage(fe validator.FieldError) string {
	label := strings.ToUpper(fe.Field()[:1]) + fe.Field()[1:]
	switch fe.Tag() {
	case "required":
		return label + " is required"
	case "max":
		return label + " must be at most " + fe.Param() + " characters"
	default:
		return label + " is invalid"
	}
}

// FormInvalid re-renders a form partial with the submitted values and
// per-field errors attached, as HTTP 422 — paired with the
// htmx.config.responseHandling override in scripts.html that swaps 422
// responses normally (target + any OOB fragments) instead of htmx's default
// "4xx: no swap" handling. JSON clients get {"ok":false,"errors":{...}}.
func FormInvalid(c *fiber.Ctx, template string, data fiber.Map, errs FieldErrors) error {
	c.Status(fiber.StatusUnprocessableEntity)
	if IsHTMX(c) {
		if data == nil {
			data = fiber.Map{}
		}
		data["Errors"] = errs
		return c.Render(template, data)
	}
	return c.JSON(fiber.Map{"ok": false, "errors": errs})
}
