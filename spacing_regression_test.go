package pdfgrab

import (
	"os"
	"strings"
	"testing"
)

// allText extracts every page of a fixture with DefaultTextOpts and joins it.
func allText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc, err := OpenBytes(b)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer doc.Close()

	var sb strings.Builder
	for p := 1; p <= doc.NumPages(); p++ {
		pg, err := doc.Page(p)
		if err != nil {
			t.Fatalf("page %d: %v", p, err)
		}
		txt, err := pg.ExtractText(DefaultTextOpts())
		if err != nil {
			t.Fatalf("extract page %d: %v", p, err)
		}
		sb.WriteString(txt)
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestWordSpacingRegression guards the extraction quality on real, tightly-set
// design PDFs (the Vectorless whitepaper + technical docs). Before the
// size-relative word tolerance + explicit-space handling, body text merged
// into runs like "Vectorsarefuzzy" / "whatevershapeyourstackprefers".
func TestWordSpacingRegression(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		mustHave []string // correctly-spaced phrases
		mustNot  []string // old garbled (merged) forms
	}{
		{
			name: "whitepaper",
			path: "testdata/vectorless-whitepaper.pdf",
			mustHave: []string{
				"Vectors are fuzzy nearest-neighbour guesses",
				"In courtrooms, clinics, and capital",
				"One Go binary. Self-hosted",
			},
			mustNot: []string{"Vectorsarefuzzy", "Incourtrooms"},
		},
		{
			name: "techdocs",
			path: "testdata/vectorless-techdocs.pdf",
			mustHave: []string{
				"Five components. One retrieval primitive",
				"whatever shape your stack prefers",
				"System overview",
			},
			mustNot: []string{"whatevershapeyourstackprefers", "Fivecomponents"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := allText(t, tc.path)

			if n := strings.Count(text, "�"); n > 0 {
				t.Errorf("%s: found %d U+FFFD replacement chars — glyph decoding failed", tc.name, n)
			}
			for _, want := range tc.mustHave {
				if !strings.Contains(text, want) {
					t.Errorf("%s: expected correctly-spaced phrase %q, not found", tc.name, want)
				}
			}
			for _, bad := range tc.mustNot {
				if strings.Contains(text, bad) {
					t.Errorf("%s: found merged/garbled run %q — word spacing regressed", tc.name, bad)
				}
			}
		})
	}
}
