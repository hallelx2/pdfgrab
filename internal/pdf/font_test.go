// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdf

import "testing"

// TestEncodingByName checks that the four base PDF encodings produce
// the correct printable-ASCII mapping and the encoding-specific slots
// outside that range.
//
// Important: identity over 0x20..0x7e holds for WinAnsi, MacRoman, and
// PDFDoc, but NOT for StandardEncoding — per PDF Reference 1.7
// Appendix D.2, StandardEncoding maps 0x27 to "quoteright" (U+2019)
// and 0x60 to "quoteleft" (U+2018), not ASCII apostrophe/backtick.
// This is the bug the v0.1.1 fix corrects (the previous table was
// ASCII-identity over the printable range and silently dropped curly
// quotes / dashes / ligatures on real PDFs).
func TestEncodingByName(t *testing.T) {
	for _, name := range []string{"WinAnsiEncoding", "MacRomanEncoding", "PDFDocEncoding"} {
		tab := EncodingByName(name)
		for c := byte(0x20); c < 0x7f; c++ {
			if tab[c] != string(rune(c)) {
				t.Errorf("%s[0x%02x] = %q, want %q", name, c, tab[c], string(rune(c)))
			}
		}
	}
	// StandardEncoding: identity except the typographic-quote slots.
	std := EncodingByName("StandardEncoding")
	for c := byte(0x20); c < 0x7f; c++ {
		if c == 0x27 || c == 0x60 {
			continue
		}
		if std[c] != string(rune(c)) {
			t.Errorf("StandardEncoding[0x%02x] = %q, want %q", c, std[c], string(rune(c)))
		}
	}
	if std[0x27] != "’" {
		t.Errorf("StandardEncoding[0x27] = %q, want quoteright (’)", std[0x27])
	}
	if std[0x60] != "‘" {
		t.Errorf("StandardEncoding[0x60] = %q, want quoteleft (‘)", std[0x60])
	}
	// WinAnsi-specific high-byte slots.
	wa := EncodingByName("WinAnsiEncoding")
	if wa[0x80] != "€" {
		t.Errorf("WinAnsi[0x80] = %q, want €", wa[0x80])
	}
	if wa[0x96] != "–" {
		t.Errorf("WinAnsi[0x96] = %q, want en-dash", wa[0x96])
	}
	if wa[0x97] != "—" {
		t.Errorf("WinAnsi[0x97] = %q, want em-dash", wa[0x97])
	}
	if wa[0x91] != "‘" {
		t.Errorf("WinAnsi[0x91] = %q, want quoteleft (‘)", wa[0x91])
	}
	if wa[0x92] != "’" {
		t.Errorf("WinAnsi[0x92] = %q, want quoteright (’)", wa[0x92])
	}
	if wa[0x93] != "“" {
		t.Errorf("WinAnsi[0x93] = %q, want quotedblleft (“)", wa[0x93])
	}
	if wa[0x94] != "”" {
		t.Errorf("WinAnsi[0x94] = %q, want quotedblright (”)", wa[0x94])
	}
	if wa[0x95] != "•" {
		t.Errorf("WinAnsi[0x95] = %q, want bullet (•)", wa[0x95])
	}
	if wa[0x83] != "ƒ" {
		t.Errorf("WinAnsi[0x83] = %q, want florin (ƒ)", wa[0x83])
	}
}

// TestApplyDifferences overlays a /Differences-style entry on
// StandardEncoding and asserts only the named slots changed.
func TestApplyDifferences(t *testing.T) {
	base := EncodingByName("StandardEncoding")
	diffs := []Difference{
		{CID: 39, GlyphName: "quotesingle"}, // ASCII apostrophe
		{CID: 96, GlyphName: "grave"},
	}
	out := ApplyDifferences(base, diffs)
	if out[39] != "'" {
		t.Errorf("out[39] = %q, want '", out[39])
	}
	if out[96] != "`" {
		t.Errorf("out[96] = %q, want `", out[96])
	}
	// Unaffected slot still identity.
	if out[65] != "A" {
		t.Errorf("out[65] = %q, want A", out[65])
	}
}

// TestAdobeGlyphRecognisers checks the uni-prefix and u-prefix
// recognisers since the table is small and most real fonts emit
// glyphs via these forms.
func TestAdobeGlyphRecognisers(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"A", "A"},
		{"comma", ","},
		// "fi" is the AGL ligature glyph (U+FB01), NOT the two-letter
		// string "f"+"i". This is what pdfminer/pdfplumber return; the
		// pre-v0.1.1 table missed this and returned "" (then fell back
		// to a (cid:NNN) placeholder).
		{"fi", "ﬁ"},
		{"fl", "ﬂ"},
		{"quoteleft", "‘"},
		{"quoteright", "’"},
		{"quotedblleft", "“"},
		{"quotedblright", "”"},
		{"endash", "–"},
		{"emdash", "—"},
		{"bullet", "•"},
		{"florin", "ƒ"},
		// Compound name (AGL §2): "f_i" decomposes to its parts.
		{"f_i", "fi"},
		// Variant suffix is stripped (AGL §2): "A.alt" → "A".
		{"A.alt", "A"},
		{"uni0041", "A"},
		{"uni004100420043", "ABC"},
		{"u0041", "A"},
		{"u1F600", "😀"},
		{"notaknownname", ""},
	}
	for _, tc := range cases {
		got := AdobeGlyphToUnicode(tc.in)
		if got != tc.want {
			t.Errorf("AdobeGlyphToUnicode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFontDecodeUnicodeFallback verifies the lookup priority:
// ToUnicode > encoding > Latin-1 > (cid:NNN).
func TestFontDecodeUnicodeFallback(t *testing.T) {
	enc := EncodingByName("WinAnsiEncoding")
	f := &Font{
		BaseFont:            "TestFont",
		IsSimple:            true,
		cid2unicodeEncoding: enc,
	}
	// Encoding hit.
	if got := f.DecodeUnicode(0x41); got != "A" {
		t.Errorf("DecodeUnicode(0x41) = %q, want A", got)
	}
	// Latin-1 fallback for an unmapped printable byte.
	f.cid2unicodeEncoding[0x42] = "" // clear B
	if got := f.DecodeUnicode(0x42); got != "B" {
		t.Errorf("DecodeUnicode(0x42) fallback = %q, want B", got)
	}
	// Out of range: (cid:NNN).
	if got := f.DecodeUnicode(0x100); got != "(cid:256)" {
		t.Errorf("DecodeUnicode(0x100) = %q, want (cid:256)", got)
	}

	// ToUnicode takes priority over the encoding table.
	tu := NewCMap()
	tu.cid2unicode[0x41] = "Z"
	f.ToUnicode = tu
	if got := f.DecodeUnicode(0x41); got != "Z" {
		t.Errorf("DecodeUnicode(0x41) with ToUnicode = %q, want Z", got)
	}
}
