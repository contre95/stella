package review

import (
	"html/template"
	"os"
	"path/filepath"
	"testing"

	"github.com/contre95/stella/src/features/hosting"
)

// TestReviewTemplatesParse guards the template syntax of the review views,
// which are only parsed at runtime by the Fiber view engine.
func TestReviewTemplatesParse(t *testing.T) {
	for _, rel := range []string{
		"views/partials/navbar.html",
		"views/partials/review_card.html",
		"views/partials/scripts.html",
		"views/sections/review.html",
		"views/sections/review_done.html",
		"views/sections/cards.html",
		"views/components/deck_facts.html",
	} {
		b, err := os.ReadFile(filepath.Join("../../..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if _, err := template.New(rel).Funcs(template.FuncMap{"md": hosting.MarkdownHTML}).Parse(string(b)); err != nil {
			t.Errorf("parse %s: %v", rel, err)
		}
	}
}
