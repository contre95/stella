package review

import (
	"fmt"
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
		"views/sections/study_pick.html",
		"views/sections/deck_page.html",
		"views/components/deck_facts.html",
		"views/components/deck_list.html",
		"views/components/card_list.html",
		"views/components/card_preview.html",
		"views/components/deck_card_grid.html",
		"views/partials/icons.html",
	} {
		b, err := os.ReadFile(filepath.Join("../../..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		funcs := template.FuncMap{
			"md":  hosting.MarkdownHTML,
			"add": func(a, b int) int { return a + b },
			"sub": func(a, b int) int { return a - b },
			"dict": func(values ...any) (map[string]any, error) {
				if len(values)%2 != 0 {
					return nil, fmt.Errorf("dict requires an even number of arguments")
				}
				m := make(map[string]any, len(values)/2)
				for i := 0; i < len(values); i += 2 {
					key, ok := values[i].(string)
					if !ok {
						return nil, fmt.Errorf("dict keys must be strings")
					}
					m[key] = values[i+1]
				}
				return m, nil
			},
			"pluralS": func(n int) string {
				if n == 1 {
					return ""
				}
				return "s"
			},
		}
		if _, err := template.New(rel).Funcs(funcs).Parse(string(b)); err != nil {
			t.Errorf("parse %s: %v", rel, err)
		}
	}
}
