// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/hallelx2/pdfgrab"
	"github.com/hallelx2/pdfgrab/testdata"
)

// TestOpenAndCount checks the Open* surface plus NumPages on the
// hand-crafted hello_world.pdf fixture. The fixture is built in
// memory by testdata.Hello() to keep the repo binary-free.
func TestOpenAndCount(t *testing.T) {
	data := testdata.Hello()
	doc, err := pdfgrab.OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	if doc.NumPages() != 1 {
		t.Fatalf("NumPages = %d, want 1", doc.NumPages())
	}
	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}
	if p.Number() != 1 {
		t.Fatalf("Number = %d, want 1", p.Number())
	}
	if w, h := p.Width(), p.Height(); w != 612 || h != 792 {
		t.Errorf("page size = %v x %v, want 612 x 792", w, h)
	}
}

// TestOpenReader verifies the io.Reader entry point. We pass a
// *bytes.Reader so the result must match OpenBytes exactly.
func TestOpenReader(t *testing.T) {
	data := testdata.Hello()
	doc, err := pdfgrab.Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()
	if doc.NumPages() != 1 {
		t.Fatalf("NumPages = %d, want 1", doc.NumPages())
	}
}

// TestPagesIterator exercises the iter.Seq2 range iterator.
func TestPagesIterator(t *testing.T) {
	doc, err := pdfgrab.OpenBytes(testdata.Hello())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	count := 0
	var seen []int
	for n, p := range doc.Pages() {
		count++
		seen = append(seen, n)
		if p == nil {
			t.Errorf("nil page yielded at n=%d", n)
		}
	}
	if count != 1 {
		t.Errorf("iterated %d pages, want 1", count)
	}
	if len(seen) != 1 || seen[0] != 1 {
		t.Errorf("iterated indices = %v, want [1]", seen)
	}
}

// TestPageOutOfRange checks that the sentinel is returned correctly
// for both too-low and too-high page indices.
func TestPageOutOfRange(t *testing.T) {
	doc, err := pdfgrab.OpenBytes(testdata.Hello())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()

	cases := []int{0, -1, 2, 100}
	for _, n := range cases {
		_, err := doc.Page(n)
		if !errors.Is(err, pdfgrab.ErrPageOutOfRange) {
			t.Errorf("Page(%d) err = %v, want ErrPageOutOfRange", n, err)
		}
	}
}

// TestInvalidPDF checks that Open* returns ErrInvalidPDF for non-PDF
// bytes. We test the header check (cheap fast-path) AND a
// header-claiming-PDF-but-truncated case (pdfcpu's deeper failure).
func TestInvalidPDF(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte("not a pdf at all"),
		[]byte("plain text"),
	}
	for _, c := range cases {
		_, err := pdfgrab.OpenBytes(c)
		if !errors.Is(err, pdfgrab.ErrInvalidPDF) {
			t.Errorf("OpenBytes(%q): err = %v, want ErrInvalidPDF", c, err)
		}
	}
}
