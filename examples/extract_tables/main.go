// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

// examples/extract_tables/main.go is the runnable form of the
// README's "Tables (lines strategy)" example. It exists so that
// changes to the public API surface break the example at build time
// rather than letting a stale snippet drift in the README.
//
// Run from the repo root:
//
//	go run ./examples/extract_tables testdata/golden/issue-466-example.pdf
//
// The example uses the ExtractTables call with default settings
// (which select the "lines" strategy on both axes). It prints each
// detected table's rows × cols and dimensions, then each row as a
// flat slice — exactly the snippet documented in README.md.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/hallelx2/pdfgrab"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: extract_tables <file.pdf>")
		os.Exit(2)
	}
	path := os.Args[1]

	doc, err := pdfgrab.OpenFile(path)
	if err != nil {
		log.Fatalf("OpenFile %s: %v", path, err)
	}
	defer doc.Close()

	page, err := doc.Page(1)
	if err != nil {
		log.Fatalf("Page(1): %v", err)
	}

	settings := pdfgrab.DefaultTableSettings()
	// Uncomment to ignore Rect outlines (filled cell backgrounds
	// that aren't real row boundaries):
	// settings.VerticalStrategy = pdfgrab.StrategyLinesStrict

	tables, err := page.ExtractTables(settings)
	if err != nil {
		log.Fatalf("ExtractTables: %v", err)
	}
	for ti, t := range tables {
		cols := 0
		if len(t.Rows) > 0 {
			cols = len(t.Rows[0])
		}
		fmt.Printf("table %d: %d rows × %d cols at %+v\n",
			ti, len(t.Rows), cols, t.BBox)
		for _, row := range t.Rows {
			fmt.Println(row)
		}
	}
}
