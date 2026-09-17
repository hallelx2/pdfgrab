// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdf

import (
	"testing"
)

// TestToUnicodeBfchar parses a minimal ToUnicode CMap with three
// bfchar entries and one bfrange and asserts that lookups return the
// expected Unicode strings.
//
// The CMap fragment below is a typical minimal ToUnicode stream as
// emitted by modern PDF generators. We strip the surrounding
// /CIDInit / proc-set scaffolding because our parser ignores it
// anyway — only bfchar/bfrange contribute mappings.
func TestToUnicodeBfchar(t *testing.T) {
	src := []byte(`
3 beginbfchar
<0041> <0041>
<0042> <0042>
<00a0> <0020>
endbfchar
1 beginbfrange
<0030> <0039> <0030>
endbfrange
`)
	c, err := ParseCMap(src)
	if err != nil {
		t.Fatalf("ParseCMap: %v", err)
	}
	cases := []struct {
		cid  uint16
		want string
	}{
		{0x0041, "A"},
		{0x0042, "B"},
		{0x00a0, " "}, // non-breaking space remapped to ASCII space
		{0x0030, "0"},
		{0x0033, "3"},
		{0x0039, "9"},
	}
	for _, tc := range cases {
		got, ok := c.Lookup(tc.cid)
		if !ok {
			t.Errorf("Lookup(%04x): not found", tc.cid)
			continue
		}
		if got != tc.want {
			t.Errorf("Lookup(%04x) = %q, want %q", tc.cid, got, tc.want)
		}
	}

	// Out-of-range lookups return false.
	if _, ok := c.Lookup(0xFFFF); ok {
		t.Error("Lookup(0xFFFF) found, want missing")
	}
}

// TestCMapBFRangeArray exercises the second form of bfrange where
// the destination is an array — used for ligatures and other multi-
// character glyph mappings.
func TestCMapBFRangeArray(t *testing.T) {
	src := []byte(`
1 beginbfrange
<0001> <0003> [ <0066> <00660069> <00660066> ]
endbfrange
`)
	c, err := ParseCMap(src)
	if err != nil {
		t.Fatalf("ParseCMap: %v", err)
	}
	cases := []struct {
		cid  uint16
		want string
	}{
		{0x0001, "f"},
		{0x0002, "fi"},
		{0x0003, "ff"},
	}
	for _, tc := range cases {
		got, ok := c.Lookup(tc.cid)
		if !ok || got != tc.want {
			t.Errorf("Lookup(%04x) = (%q, %v), want (%q, true)", tc.cid, got, ok, tc.want)
		}
	}
}

// TestCMapTokenizer is a smoke test for the lexer — ensures the
// hex/string/keyword classification is correct on a small example.
func TestCMapTokenizer(t *testing.T) {
	toks := tokenizeCMap([]byte(`<0041> /Name 12 keyword [foo]`))
	if len(toks) < 5 {
		t.Fatalf("got %d tokens, want >= 5", len(toks))
	}
	if toks[0].kind != tokHex {
		t.Errorf("toks[0] kind = %v, want hex", toks[0].kind)
	}
	if toks[1].kind != tokName || toks[1].text != "Name" {
		t.Errorf("toks[1] = %+v, want /Name", toks[1])
	}
	if toks[2].kind != tokNumber || toks[2].text != "12" {
		t.Errorf("toks[2] = %+v, want 12", toks[2])
	}
	if toks[3].kind != tokKeyword || toks[3].text != "keyword" {
		t.Errorf("toks[3] = %+v, want keyword", toks[3])
	}
	if toks[4].kind != tokArrayBegin {
		t.Errorf("toks[4] kind = %v, want array-begin", toks[4].kind)
	}
}

// TestHexBytesToUTF16BE checks the BMP and surrogate-pair decoding.
func TestHexBytesToUTF16BE(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0x00, 0x41}, "A"},                      // BMP
		{[]byte{0x00, 0x66, 0x00, 0x69}, "fi"},         // two BMP code units
		{[]byte{0xD8, 0x3D, 0xDE, 0x00}, "\U0001F600"}, // surrogate pair → 😀
	}
	for _, tc := range cases {
		got := hexBytesToUTF16BE(tc.in)
		if got != tc.want {
			t.Errorf("hexBytesToUTF16BE(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
