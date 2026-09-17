// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

// Package pdfgrab is a Go-native port of Python's pdfplumber. It
// reads a PDF document, walks the content streams, and surfaces the
// positioned primitives (characters, lines, rectangles, curves) that
// higher-level layout algorithms — text extraction, word grouping,
// table detection — operate on.
//
// The library is structured in layers that mirror the pdfplumber +
// pdfminer.six split:
//
//   - This package (pdfgrab) is the public API. It is small, stable,
//     and contains no PDF parsing logic of its own — it just exposes
//     Document, Page, Char, Line, Rect, Curve and constructs them
//     from the internal package.
//   - github.com/hallelx2/pdfgrab/internal/pdf is the content-stream
//     interpreter. It is intentionally not public: the data shapes
//     there will evolve as we add more PDF features, and we don't
//     want callers depending on them.
//
// Typical usage:
//
//	doc, err := pdfgrab.OpenFile("report.pdf")
//	if err != nil { return err }
//	defer doc.Close()
//
//	for n, p := range doc.Pages() {
//	    chars, _ := p.Chars()
//	    fmt.Printf("page %d: %d chars\n", n, len(chars))
//	}
//
// Phase scope: v0.1.0 ships content-stream primitives plus text
// extraction (Page.Words, Page.ExtractText, Page.ExtractTextSimple).
// Table-finding (ExtractTables, FindTables) is the next phase — see
// the README for the roadmap. The Page interface is additive across
// releases; v0.0.1 callers using only Chars/Lines/Rects/Curves
// continue to compile against v0.1.0 without changes.
package pdfgrab

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hallelx2/pdfgrab/internal/pdf"
)

// Open reads an entire io.Reader into memory and parses it as a PDF.
// pdfcpu's API requires an io.ReadSeeker; we buffer the input so the
// caller doesn't have to.
//
// Use OpenBytes if you already have the file content as a []byte
// (avoids an extra copy), or OpenFile if you have a path.
func Open(r io.Reader) (Document, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil reader", ErrInvalidPDF)
	}
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrInvalidPDF, err)
	}
	return OpenBytes(buf)
}

// OpenBytes parses an in-memory PDF.
//
// The bytes are NOT copied — pdfcpu reads from them in-place and may
// retain references after this function returns. Callers must not
// mutate the slice for as long as the returned Document is in use.
func OpenBytes(b []byte) (Document, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrInvalidPDF)
	}
	// Quick sanity check: PDFs start with "%PDF-" (PDF 1.7 §7.5.2).
	// The check is cheap and lets us return a clearer error than
	// pdfcpu's "could not find xref" deep failure.
	if !bytes.HasPrefix(bytes.TrimLeft(b, "\x00\r\n\t "), []byte("%PDF-")) {
		return nil, fmt.Errorf("%w: missing %%PDF- header", ErrInvalidPDF)
	}
	r, err := pdf.NewReader(b)
	if err != nil {
		// pdfcpu encryption errors are pretty distinctive — surface
		// our cleaner sentinel so callers can branch on it.
		if isEncryptedErr(err) {
			return nil, fmt.Errorf("%w: %v", ErrEncrypted, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidPDF, err)
	}
	return &document{reader: r}, nil
}

// OpenFile opens a PDF at the given filesystem path.
func OpenFile(path string) (Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pdfgrab: open %s: %w", path, err)
	}
	return OpenBytes(data)
}

// isEncryptedErr is a heuristic: pdfcpu doesn't expose a sentinel
// for encryption-related parse failures, but the messages are
// distinctive. We match on lower-cased substrings to be tolerant
// across pdfcpu versions.
func isEncryptedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, sig := range []string{"encrypt", "password", "permission"} {
		if containsFold(msg, sig) {
			return true
		}
	}
	return false
}

func containsFold(s, substr string) bool {
	// Tiny ASCII-only case-insensitive contains. Keeps the package
	// stdlib-free of strings.ToLower allocation when we run this on
	// every error path.
	if len(substr) == 0 || len(substr) > len(s) {
		return false
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			a := s[i+j]
			b := substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Compile-time assertions: ensure our concrete types satisfy the
// public interfaces. If a field ever drifts out from under us, the
// build fails here rather than at a caller's call site.
var (
	_ Document = (*document)(nil)
	_ Page     = (*page)(nil)
)

// errIs is a tiny re-export so callers that vendor pdfgrab into a
// monorepo don't have to also import "errors" for the common case of
// matching one of our sentinels.
func errIs(err, target error) bool { return errors.Is(err, target) }
