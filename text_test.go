// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"math"
	"strings"
	"testing"
)

// TestExtractWordsBasic builds a hand-crafted slice of Chars for two
// words on one line and checks that the word grouper:
//  1. produces exactly two words,
//  2. keeps them in left-to-right order,
//  3. concatenates the right text,
//  4. unions the bbox correctly.
//
// We bypass the PDF pipeline (Page.Chars) and feed the algorithm
// directly so the test stays deterministic regardless of font metrics.
func TestExtractWordsBasic(t *testing.T) {
	chars := []Char{
		{Text: "H", X0: 10, Y0: 100, X1: 18, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "i", X0: 18, Y0: 100, X1: 21, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		// Big gap after "Hi" → new word.
		{Text: "T", X0: 50, Y0: 100, X1: 58, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "h", X0: 58, Y0: 100, X1: 64, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "e", X0: 64, Y0: 100, X1: 70, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "r", X0: 70, Y0: 100, X1: 73, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "e", X0: 73, Y0: 100, X1: 79, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
	}

	opts := DefaultWordOpts()
	words := extractWordsFromChars(chars, opts)

	if len(words) != 2 {
		t.Fatalf("got %d words, want 2", len(words))
	}
	if words[0].Text != "Hi" {
		t.Errorf("word 0 = %q, want %q", words[0].Text, "Hi")
	}
	if words[1].Text != "There" {
		t.Errorf("word 1 = %q, want %q", words[1].Text, "There")
	}
	if !approxFloat(words[0].X0, 10, 0.01) || !approxFloat(words[0].X1, 21, 0.01) {
		t.Errorf("word 0 bbox X = (%v, %v), want (10, 21)", words[0].X0, words[0].X1)
	}
	if words[0].Direction != "ltr" {
		t.Errorf("word 0 direction = %q, want ltr", words[0].Direction)
	}
}

func TestExtractWordsMultipleLines(t *testing.T) {
	// Two lines, two words each. Y values are widely separated.
	chars := []Char{
		// Line 1: "Hello world" at Y~100
		{Text: "H", X0: 10, Y0: 100, X1: 18, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "e", X0: 18, Y0: 100, X1: 24, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "l", X0: 24, Y0: 100, X1: 27, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "l", X0: 27, Y0: 100, X1: 30, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "o", X0: 30, Y0: 100, X1: 36, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "w", X0: 50, Y0: 100, X1: 60, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "o", X0: 60, Y0: 100, X1: 66, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "r", X0: 66, Y0: 100, X1: 69, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "l", X0: 69, Y0: 100, X1: 72, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		{Text: "d", X0: 72, Y0: 100, X1: 78, Y1: 112, Upright: true, FontName: "F", FontSize: 12},
		// Line 2: "Foo bar" at Y~80
		{Text: "F", X0: 10, Y0: 80, X1: 18, Y1: 92, Upright: true, FontName: "F", FontSize: 12},
		{Text: "o", X0: 18, Y0: 80, X1: 24, Y1: 92, Upright: true, FontName: "F", FontSize: 12},
		{Text: "o", X0: 24, Y0: 80, X1: 30, Y1: 92, Upright: true, FontName: "F", FontSize: 12},
		{Text: "b", X0: 40, Y0: 80, X1: 46, Y1: 92, Upright: true, FontName: "F", FontSize: 12},
		{Text: "a", X0: 46, Y0: 80, X1: 52, Y1: 92, Upright: true, FontName: "F", FontSize: 12},
		{Text: "r", X0: 52, Y0: 80, X1: 55, Y1: 92, Upright: true, FontName: "F", FontSize: 12},
	}

	words := extractWordsFromChars(chars, DefaultWordOpts())
	got := make([]string, len(words))
	for i, w := range words {
		got[i] = w.Text
	}
	want := []string{"Hello", "world", "Foo", "bar"}
	if len(got) != len(want) {
		t.Fatalf("got %d words %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("word %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractWordsBlankChars(t *testing.T) {
	// Explicit space char between two words — by default it should be
	// dropped (KeepBlankChars=false).
	chars := []Char{
		{Text: "A", X0: 10, Y0: 100, X1: 18, Y1: 112, Upright: true},
		{Text: " ", X0: 18, Y0: 100, X1: 22, Y1: 112, Upright: true},
		{Text: "B", X0: 22, Y0: 100, X1: 30, Y1: 112, Upright: true},
	}
	opts := DefaultWordOpts()
	words := extractWordsFromChars(chars, opts)

	// "A" and "B" are 4pt apart (18→22), which is > XTolerance(3), so
	// they're separate words. Without the space char, just two words.
	if len(words) != 2 {
		t.Fatalf("got %d words, want 2: %+v", len(words), words)
	}

	// With KeepBlankChars=true the algorithm sees the space and splits.
	opts.KeepBlankChars = true
	words = extractWordsFromChars(chars, opts)
	if len(words) != 2 {
		t.Fatalf("got %d words with KeepBlankChars, want 2", len(words))
	}
}

func TestExtractWordsLigatureExpansion(t *testing.T) {
	// A ligature char (ﬁ U+FB01) followed by "le" should expand to
	// "file" when Expand=true.
	chars := []Char{
		{Text: "ﬁ", X0: 10, Y0: 100, X1: 18, Y1: 112, Upright: true},
		{Text: "l", X0: 18, Y0: 100, X1: 21, Y1: 112, Upright: true},
		{Text: "e", X0: 21, Y0: 100, X1: 27, Y1: 112, Upright: true},
	}
	opts := DefaultWordOpts()
	words := extractWordsFromChars(chars, opts)
	if len(words) != 1 {
		t.Fatalf("got %d words, want 1", len(words))
	}
	if words[0].Text != "file" {
		t.Errorf("got %q, want %q", words[0].Text, "file")
	}

	opts.Expand = false
	words = extractWordsFromChars(chars, opts)
	if words[0].Text != "ﬁle" {
		t.Errorf("got %q, want %q (no expansion)", words[0].Text, "ﬁle")
	}
}

func TestExtractWordsSplitAtPunctuation(t *testing.T) {
	// "ABC,DEF" with SplitAtPunctuation=true → three words: ABC , DEF.
	chars := []Char{
		{Text: "A", X0: 10, Y0: 100, X1: 18, Y1: 112, Upright: true},
		{Text: "B", X0: 18, Y0: 100, X1: 26, Y1: 112, Upright: true},
		{Text: "C", X0: 26, Y0: 100, X1: 34, Y1: 112, Upright: true},
		{Text: ",", X0: 34, Y0: 100, X1: 38, Y1: 112, Upright: true},
		{Text: "D", X0: 38, Y0: 100, X1: 46, Y1: 112, Upright: true},
		{Text: "E", X0: 46, Y0: 100, X1: 54, Y1: 112, Upright: true},
		{Text: "F", X0: 54, Y0: 100, X1: 62, Y1: 112, Upright: true},
	}
	opts := DefaultWordOpts()
	opts.SplitAtPunctuation = true
	words := extractWordsFromChars(chars, opts)
	wantTexts := []string{"ABC", ",", "DEF"}
	if len(words) != len(wantTexts) {
		t.Fatalf("got %d words, want %d", len(words), len(wantTexts))
	}
	for i, w := range words {
		if w.Text != wantTexts[i] {
			t.Errorf("word %d = %q, want %q", i, w.Text, wantTexts[i])
		}
	}
}

func TestExtractWordsKeepChars(t *testing.T) {
	chars := []Char{
		{Text: "A", X0: 10, Y0: 100, X1: 18, Y1: 112, Upright: true},
		{Text: "B", X0: 18, Y0: 100, X1: 26, Y1: 112, Upright: true},
	}
	opts := DefaultWordOpts()
	opts.KeepChars = true
	words := extractWordsFromChars(chars, opts)
	if len(words) != 1 || len(words[0].Chars) != 2 {
		t.Fatalf("got %d words, %d chars; want 1 word with 2 chars", len(words), len(words[0].Chars))
	}

	// And when KeepChars=false, Chars should be nil.
	opts.KeepChars = false
	words = extractWordsFromChars(chars, opts)
	if words[0].Chars != nil {
		t.Errorf("Chars should be nil when KeepChars=false, got %v", words[0].Chars)
	}
}

func TestExtractTextDense(t *testing.T) {
	chars := []Char{
		{Text: "H", X0: 10, Y0: 100, X1: 18, Y1: 112, Upright: true},
		{Text: "i", X0: 18, Y0: 100, X1: 21, Y1: 112, Upright: true},
		{Text: "T", X0: 50, Y0: 100, X1: 58, Y1: 112, Upright: true},
		{Text: "h", X0: 58, Y0: 100, X1: 64, Y1: 112, Upright: true},
		{Text: "e", X0: 64, Y0: 100, X1: 70, Y1: 112, Upright: true},
		{Text: "r", X0: 70, Y0: 100, X1: 73, Y1: 112, Upright: true},
		{Text: "e", X0: 73, Y0: 100, X1: 79, Y1: 112, Upright: true},
		// Second line.
		{Text: "Y", X0: 10, Y0: 80, X1: 18, Y1: 92, Upright: true},
	}
	opts := DefaultTextOpts()
	got := extractTextFromChars(chars, opts)
	want := "Hi There\nY"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractTextEmpty(t *testing.T) {
	opts := DefaultTextOpts()
	if got := extractTextFromChars(nil, opts); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextLayoutPreservesIndentation(t *testing.T) {
	// Two lines with different left-indents on a 612x792 page (US
	// letter). After layout=true, the second line should appear
	// indented relative to the first.
	chars := []Char{
		// Line 1 starts near the left margin, ~x=72.
		{Text: "A", X0: 72, Y0: 700, X1: 80, Y1: 712, Upright: true},
		{Text: "B", X0: 80, Y0: 700, X1: 88, Y1: 712, Upright: true},
		// Line 2 deeply indented, ~x=200.
		{Text: "C", X0: 200, Y0: 680, X1: 208, Y1: 692, Upright: true},
		{Text: "D", X0: 208, Y0: 680, X1: 216, Y1: 692, Upright: true},
	}
	opts := DefaultTextOpts()
	opts.Layout = true
	opts.LayoutWidthChars = 80
	opts.LayoutHeightChars = 60
	out := extractTextWithLayout(chars, 612, 792, opts)
	if !strings.Contains(out, "AB") {
		t.Errorf("layout text missing AB run: %q", out)
	}
	if !strings.Contains(out, "CD") {
		t.Errorf("layout text missing CD run: %q", out)
	}
	// Find the lines: AB should be at a column < CD's column.
	lines := strings.Split(out, "\n")
	var abCol, cdCol int = -1, -1
	for _, l := range lines {
		if i := strings.Index(l, "AB"); i >= 0 && abCol < 0 {
			abCol = i
		}
		if i := strings.Index(l, "CD"); i >= 0 {
			cdCol = i
		}
	}
	if abCol < 0 || cdCol < 0 {
		t.Fatalf("expected to find AB and CD on lines: %q", out)
	}
	if cdCol <= abCol {
		t.Errorf("CD column (%d) should be > AB column (%d): %q", cdCol, abCol, out)
	}
}

func TestPageExtractTextLayout(t *testing.T) {
	doc, err := openHelloWorldDoc()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	opts := DefaultTextOpts()
	opts.Layout = true
	got, err := p.ExtractText(opts)
	if err != nil {
		t.Fatalf("ExtractText layout: %v", err)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "world") {
		t.Errorf("layout text missing expected substrings: %q", got)
	}
}

// TestPageWordsHelloWorld walks the Hello-world fixture through the
// public Page.Words API and asserts one word matching "Hello," is
// followed by one matching "world!".
func TestPageWordsHelloWorld(t *testing.T) {
	doc, err := openHelloWorldDoc()
	if err != nil {
		t.Fatalf("open hello: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	words, err := p.Words(DefaultWordOpts())
	if err != nil {
		t.Fatalf("Words: %v", err)
	}
	if len(words) != 2 {
		t.Fatalf("got %d words, want 2: %+v", len(words), words)
	}
	if words[0].Text != "Hello," || words[1].Text != "world!" {
		t.Errorf("got %q, %q; want %q, %q",
			words[0].Text, words[1].Text, "Hello,", "world!")
	}
	// Words inherit font metadata from their first char.
	if words[0].FontName != "Helvetica" {
		t.Errorf("FontName = %q, want Helvetica", words[0].FontName)
	}
	if words[0].FontSize != 12 {
		t.Errorf("FontSize = %v, want 12", words[0].FontSize)
	}
	if words[0].Direction != "ltr" {
		t.Errorf("Direction = %q, want ltr", words[0].Direction)
	}
}

func TestPageExtractTextHelloWorld(t *testing.T) {
	doc, err := openHelloWorldDoc()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	got, err := p.ExtractText(DefaultTextOpts())
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if got != "Hello, world!" {
		t.Errorf("got %q, want %q", got, "Hello, world!")
	}
}

func TestPageExtractTextSimple(t *testing.T) {
	doc, err := openHelloWorldDoc()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	got, err := p.ExtractTextSimple(3, 3)
	if err != nil {
		t.Fatalf("ExtractTextSimple: %v", err)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "world") {
		t.Errorf("ExtractTextSimple result = %q, missing expected substrings", got)
	}
}

func TestDirectionFor(t *testing.T) {
	tests := []struct {
		upright, ltr, ttb bool
		want              string
	}{
		{true, true, true, "ltr"},
		{true, false, true, "rtl"},
		{false, true, true, "ttb"},
		{false, true, false, "btt"},
	}
	for _, tt := range tests {
		got := directionFor(tt.upright, tt.ltr, tt.ttb)
		if got != tt.want {
			t.Errorf("directionFor(%v,%v,%v) = %q, want %q",
				tt.upright, tt.ltr, tt.ttb, got, tt.want)
		}
	}
}

func TestDefaultOpts(t *testing.T) {
	w := DefaultWordOpts()
	if w.XTolerance != 3 || w.YTolerance != 3 {
		t.Errorf("DefaultWordOpts tolerances = (%v,%v), want (3,3)", w.XTolerance, w.YTolerance)
	}
	if !w.HorizontalLTR || !w.VerticalTTB || !w.Expand {
		t.Errorf("DefaultWordOpts bool flags wrong: %+v", w)
	}

	x := DefaultTextOpts()
	if x.XDensity != 7.25 || x.YDensity != 13 {
		t.Errorf("DefaultTextOpts densities = (%v,%v), want (7.25, 13)", x.XDensity, x.YDensity)
	}
}

func TestIsAllSpace(t *testing.T) {
	cases := map[string]bool{
		"":     false, // matches Python's "".isspace()
		" ":    true,
		"\t":   true,
		"a":    false,
		" a ":  false,
		"\n\r": true,
	}
	for in, want := range cases {
		got := isAllSpace(in)
		if got != want {
			t.Errorf("isAllSpace(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestExpandLigatures(t *testing.T) {
	cases := map[string]string{
		"ﬀ": "ff",
		"ﬁ": "fi",
		"ﬂ": "fl",
		"ﬃ": "ffi",
		"ﬄ": "ffl",
		"ﬅ": "st",
		"ﬆ": "st",
		"A": "A", // pass-through
		"":  "",  // pass-through
	}
	for in, want := range cases {
		if got := expandLigatures(in); got != want {
			t.Errorf("expandLigatures(%q) = %q, want %q", in, got, want)
		}
	}
}

// openHelloWorldDoc opens the testdata-built hello PDF.  Pulled into a
// helper so multiple tests can share the setup without copying the
// boilerplate.
func openHelloWorldDoc() (Document, error) {
	// We import testdata indirectly via Open: that means this test
	// can't live in the _test package because testdata is in a sub-
	// directory and we're already in the pdfgrab package. Build a
	// dependency-free fixture inline instead — same structure as
	// testdata.Hello().
	return OpenBytes(helloBytes())
}

// helloBytes returns the same PDF as testdata.Hello(), inlined so this
// _test.go file inside the pdfgrab package doesn't pull in a
// dependency on testdata. (testdata/fixtures.go is in a sub-package and
// importing it here would cause a cycle.)
func helloBytes() []byte {
	return buildSinglePageBytes(`BT
/F1 12 Tf
72 720 Td
(Hello, world!) Tj
ET
`)
}

// buildSinglePageBytes builds a minimal single-page PDF whose content
// stream is the given text. Same layout as testdata.BuildSinglePage()
// but inlined to avoid the import cycle.
func buildSinglePageBytes(content string) []byte {
	const header = "%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"
	objects := []string{
		`<< /Type /Catalog /Pages 2 0 R >>`,
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> /ProcSet [/PDF /Text] >> /Contents 5 0 R >>`,
		`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`,
		"",
	}
	streamBody := []byte(content)
	objects[4] = "<< /Length " + itoa(len(streamBody)) + " >>\nstream\n" + string(streamBody) + "endstream"

	var sb strings.Builder
	sb.WriteString(header)
	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = sb.Len()
		sb.WriteString(itoa(i+1) + " 0 obj\n" + body + "\nendobj\n")
	}
	xrefPos := sb.Len()
	sb.WriteString("xref\n0 " + itoa(len(objects)+1) + "\n")
	sb.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		sb.WriteString(pad10(off) + " 00000 n \n")
	}
	sb.WriteString("trailer\n")
	sb.WriteString("<< /Size " + itoa(len(objects)+1) + " /Root 1 0 R >>\n")
	sb.WriteString("startxref\n" + itoa(xrefPos) + "\n%%EOF\n")
	return []byte(sb.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func pad10(n int) string {
	s := itoa(n)
	if len(s) >= 10 {
		return s
	}
	return strings.Repeat("0", 10-len(s)) + s
}

func approxFloat(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}
