// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab_test

import (
	"testing"

	"github.com/hallelx2/pdfgrab"
)

// closeDoc closes doc and fails the test if Close reports an error, so a
// test never discards one silently.
func closeDoc(t *testing.T, doc pdfgrab.Document) {
	t.Helper()
	if err := doc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
