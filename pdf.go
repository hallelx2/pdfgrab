// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"fmt"
	"iter"

	"github.com/hallelx2/pdfgrab/internal/pdf"
)

// Document represents one open PDF file. The interface (not a struct)
// keeps the API symmetric with Page and lets us swap implementations
// later — e.g. a Document that streams pages lazily from a remote
// blob — without breaking callers.
//
// All accessors are safe to call concurrently. The underlying pdfcpu
// *model.Context is treated as read-only after the document is opened.
type Document interface {
	// NumPages returns the page count. Always >= 1 for a valid PDF.
	NumPages() int

	// Page returns the n'th page (1-indexed). Returns
	// ErrPageOutOfRange if n is < 1 or > NumPages().
	Page(n int) (Page, error)

	// Pages returns a range iterator over (n, Page) pairs for use
	// with Go 1.23+ range-over-func syntax:
	//
	//   for n, p := range doc.Pages() {
	//       // ...
	//   }
	//
	// The iterator yields pages in 1-based order. It does NOT clone
	// the underlying Document — Close() on the doc still tears down
	// the iterator's pages.
	Pages() iter.Seq2[int, Page]

	// Close releases any resources held by the underlying parser.
	// For pdfcpu-backed documents this is currently a no-op (pdfcpu
	// holds everything in memory), but callers should still defer
	// Close() so that future backends (mmap, file handles) work.
	Close() error
}

// document is the in-process Document implementation. It owns the
// *pdf.Reader and constructs *page on demand.
type document struct {
	reader *pdf.Reader
}

func (d *document) NumPages() int { return d.reader.NumPages() }

func (d *document) Page(n int) (Page, error) {
	if n < 1 || n > d.reader.NumPages() {
		return nil, fmt.Errorf("%w: requested %d, have %d pages", ErrPageOutOfRange, n, d.reader.NumPages())
	}
	return &page{doc: d, number: n}, nil
}

func (d *document) Pages() iter.Seq2[int, Page] {
	return func(yield func(int, Page) bool) {
		n := d.reader.NumPages()
		for i := 1; i <= n; i++ {
			p := &page{doc: d, number: i}
			if !yield(i, p) {
				return
			}
		}
	}
}

func (d *document) Close() error {
	// pdfcpu's Context doesn't expose a close primitive — it holds
	// everything in memory. We keep the method on the interface so
	// future backends that hold file handles or mmap regions can
	// release them cleanly.
	d.reader = nil
	return nil
}
