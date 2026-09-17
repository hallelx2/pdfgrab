// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License. See LICENSE file in the project root.

package pdfgrab

import "errors"

// Sentinel errors returned by the public API. Callers can match these with
// errors.Is(); functions that surface a parser-level problem from pdfcpu
// or the content-stream interpreter wrap the underlying error so the cause
// is preserved.
//
// We keep this set small on purpose. The PDF spec has hundreds of failure
// modes — most of them collapse into "the bytes do not look like a PDF",
// "you asked for a page that doesn't exist", or "we don't implement this
// feature yet". Anything more specific belongs in the wrapped error string,
// not as a new sentinel.
var (
	// ErrInvalidPDF is returned by Open / OpenBytes / OpenFile when the
	// input bytes can't be parsed as a PDF. The underlying pdfcpu error
	// is wrapped so callers can still inspect the details with errors.As.
	ErrInvalidPDF = errors.New("pdfgrab: invalid PDF")

	// ErrPageOutOfRange is returned by Document.Page when n is < 1 or
	// > NumPages(). The PDF page index is 1-based, matching pdfplumber.
	ErrPageOutOfRange = errors.New("pdfgrab: page out of range")

	// ErrUnsupported is returned when we hit a PDF feature this library
	// does not yet implement (e.g. an exotic CMap, an unsupported XObject
	// subtype, vertical writing). The error string names the feature.
	ErrUnsupported = errors.New("pdfgrab: unsupported feature")

	// ErrEncrypted is returned when the PDF is encrypted and we can't
	// decrypt it with the empty password. Full encryption support is
	// out of scope for the initial release — callers that need it can
	// pre-decrypt with pdfcpu's api.Decrypt and feed the cleaned bytes
	// to OpenBytes.
	ErrEncrypted = errors.New("pdfgrab: encrypted PDF (decryption not yet supported)")
)
