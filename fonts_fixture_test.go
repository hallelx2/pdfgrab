// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab_test

import (
	"strings"
	"testing"

	"github.com/hallelx2/pdfgrab"
)

// The fixtures under testdata/fonts deliberately have NO pdfplumber
// golden. pdfplumber decodes Symbol and ZapfDingbats with
// StandardEncoding and returns Latin letters where the correct answer is
// Greek and dingbats, so a generated golden would pin the wrong answer.
// Here we assert the right answer directly.
//
// Regenerate the PDFs with: python scripts/gen_font_fixtures.py

func openPage(t *testing.T, path string, n int) pdfgrab.Page {
	t.Helper()
	doc, err := pdfgrab.OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile(%s): %v", path, err)
	}
	t.Cleanup(func() { doc.Close() })
	p, err := doc.Page(n)
	if err != nil {
		t.Fatalf("Page(%d): %v", n, err)
	}
	return p
}

// TestSymbolFixtureDecoding is the end-to-end proof for HAL-481: a real
// PDF using Symbol and ZapfDingbats, parsed through the full pipeline.
//
// Both fonts carry their own built-in encoding and neither declares
// /Encoding, so a consumer that reaches for StandardEncoding reads Latin
// letters off a page of Greek. pdfplumber 0.11.9 still does exactly that
// — it returns "abgdep" for the Symbol page and "123" for the dingbats —
// which is why this fixture is asserted here rather than against it.
func TestSymbolFixtureDecoding(t *testing.T) {
	t.Run("Symbol", func(t *testing.T) {
		p := openPage(t, "testdata/fonts/symbol.pdf", 1)
		text, err := p.ExtractText(pdfgrab.DefaultTextOpts())
		if err != nil {
			t.Fatalf("ExtractText: %v", err)
		}
		// The content stream bytes are literally "abgdep". In Symbol's
		// encoding those codes are alpha beta gamma delta epsilon pi.
		const want = "αβγδεπ"
		if !strings.Contains(text, want) {
			t.Errorf("Symbol text = %q, want it to contain %q.\n"+
				"Getting \"abgdep\" back means StandardEncoding was applied "+
				"to a font that ships its own encoding.", text, want)
		}
		if strings.Contains(text, "abgdep") {
			t.Error("Symbol decoded as Latin \"abgdep\" — built-in encoding not applied")
		}
	})

	t.Run("ZapfDingbats", func(t *testing.T) {
		p := openPage(t, "testdata/fonts/symbol.pdf", 2)
		text, err := p.ExtractText(pdfgrab.DefaultTextOpts())
		if err != nil {
			t.Fatalf("ExtractText: %v", err)
		}
		// Codes 0x31..0x33 are the a-names, not the digits 1..3.
		if strings.Contains(text, "123") {
			t.Errorf("ZapfDingbats text = %q, decoded as digits — built-in encoding not applied", text)
		}
		for _, r := range []rune{'✑', '✒', '✓'} {
			if !strings.ContainsRune(text, r) {
				t.Errorf("ZapfDingbats text = %q, missing %q", text, r)
			}
		}
	})
}

// TestDifferencesFixtureDecoding covers the resolver split from HAL-481
// through a real /Differences array.
//
// Symbol's glyph names are genuine Adobe Glyph List entries, so they mean
// the same thing in any font — a Helvetica whose /Differences names
// "Alpha" really is asking for U+0391. ZapfDingbats' "aNN" names are
// font-specific and must NOT resolve here: a Latin font naming "a1" means
// its own glyph, not U+2701 SCISSORS. Resolving those globally would
// silently corrupt text in any document that happens to use the name.
func TestDifferencesFixtureDecoding(t *testing.T) {
	p := openPage(t, "testdata/fonts/differences.pdf", 1)
	text, err := p.ExtractText(pdfgrab.DefaultTextOpts())
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}

	// A=/Alpha B=/universal C=/club E=/summation F=/partialdiff
	for _, tc := range []struct {
		glyph string
		want  rune
	}{
		{"Alpha", 'Α'},
		{"universal", '∀'},
		{"club", '♣'},
		{"summation", '∑'},
		{"partialdiff", '∂'},
	} {
		if !strings.ContainsRune(text, tc.want) {
			t.Errorf("/Differences name %q did not resolve to %q; text = %q",
				tc.glyph, tc.want, tc.want)
		}
	}

	// D=/a1 — a ZapfDingbats name in a Helvetica font. It must not become
	// the scissors dingbat.
	if strings.ContainsRune(text, '✁') {
		t.Errorf("/Differences name \"a1\" resolved to U+2701 in a Latin font; "+
			"dingbat names must stay font-scoped. text = %q", text)
	}
}

// TestStandard14FixtureCoversEveryLatinFont guards the corpus itself.
// The suite previously ran on 26 words in a single font, which is how two
// font-metric bugs survived four releases. If someone trims this fixture,
// the coverage silently collapses again — so assert the breadth, not just
// the output.
func TestStandard14FixtureCoversEveryLatinFont(t *testing.T) {
	doc, err := pdfgrab.OpenFile("testdata/golden/fonts-standard14.pdf")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	want := []string{
		"Helvetica", "Helvetica-Bold", "Helvetica-Oblique", "Helvetica-BoldOblique",
		"Times-Roman", "Times-Bold", "Times-Italic", "Times-BoldItalic",
		"Courier", "Courier-Bold", "Courier-Oblique", "Courier-BoldOblique",
	}
	if doc.NumPages() != len(want) {
		t.Fatalf("fixture has %d pages, want %d (one per Latin standard font)",
			doc.NumPages(), len(want))
	}

	seen := map[string]bool{}
	sizes := map[float64]bool{}
	for n := 1; n <= doc.NumPages(); n++ {
		p, err := doc.Page(n)
		if err != nil {
			t.Fatalf("Page(%d): %v", n, err)
		}
		chars, err := p.Chars()
		if err != nil || len(chars) == 0 {
			t.Fatalf("page %d: no chars (%v)", n, err)
		}
		for _, c := range chars {
			seen[c.FontName] = true
			sizes[c.FontSize] = true
		}
	}
	for _, f := range want {
		if !seen[f] {
			t.Errorf("fixture never renders %s", f)
		}
	}
	// Multiple sizes matter: a descent scaled by the wrong factor is
	// indistinguishable from a wrong constant at a single size.
	if len(sizes) < 3 {
		t.Errorf("fixture uses %d font sizes, want >= 3", len(sizes))
	}
}
