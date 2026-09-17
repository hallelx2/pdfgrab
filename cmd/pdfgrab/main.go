// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

// cmd/pdfgrab is the command-line interface to the pdfgrab library.
// It mirrors pdfplumber's CLI surface for the operations pdfgrab
// implements: extract text, extract tables, dump page geometry.
//
// Usage:
//
//	pdfgrab extract <file.pdf> [flags]
//
// Flags (extract subcommand):
//
//	--pages          Comma-separated page list / dash ranges, e.g. "1,3-5". Default: all.
//	--tables         Emit detected tables (JSON or text per --format).
//	--text           Emit extracted text. Mutually exclusive with --tables.
//	--format         "json" (default) | "text". Output format.
//	--vertical-strategy    "lines" (default) | "lines_strict" | "text" | "explicit".
//	--horizontal-strategy  Same set; default "lines".
//	--snap-tolerance       Float; default 3.
//	--join-tolerance       Float; default 3.
//	--edge-min-length      Float; default 3.
//	--intersection-tolerance Float; default 3.
//	--text-tolerance         Float; default 3.
//	--min-words-vertical     Int; default 3.
//	--min-words-horizontal   Int; default 1.
//	--explicit-vertical-lines    Comma-separated floats; required when vertical-strategy=explicit.
//	--explicit-horizontal-lines  Comma-separated floats; required when horizontal-strategy=explicit.
//	--indent                 Int; JSON pretty-printing indent. 0 = compact.
//
// The CLI uses the standard library `flag` package and the `pdfgrab`
// public API only — no third-party dependencies.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/hallelx2/pdfgrab"
)

// version is the CLI's reported version. The release workflow injects
// the tag via -ldflags "-X main.version=$GITHUB_REF_NAME"; a plain
// `go build` leaves it as "dev".
//
// It is deliberately NOT a hardcoded literal. It was one until v0.5.0,
// and it silently reported v0.3.0 for the whole of v0.4.0 because
// nothing tied the string to the tag.
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the testable entry point. It takes the args slice (excluding
// the executable name) and the stdout/stderr streams so tests can
// capture output without spawning a process.
func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "extract":
		return runExtract(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, "pdfgrab "+version)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

// printUsage prints the top-level usage string.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, `pdfgrab — extract text and tables from PDFs

USAGE:
  pdfgrab extract <file.pdf> [flags]
  pdfgrab version
  pdfgrab help

EXTRACT FLAGS (run 'pdfgrab extract --help' for full list):
  --pages 1,3-5            Pages to process (default: all).
  --tables                 Output detected tables.
  --text                   Output extracted text (mutually exclusive with --tables).
  --format json|text       Output format (default: json).
  --vertical-strategy S    "lines" | "lines_strict" | "text" | "explicit".
  --horizontal-strategy S  Same set, default "lines".

Documentation: https://github.com/hallelx2/pdfgrab`)
}

// extractFlags is the parsed flag set for the extract subcommand.
type extractFlags struct {
	pages                   string
	tables                  bool
	text                    bool
	format                  string
	verticalStrategy        string
	horizontalStrategy      string
	snapTolerance           float64
	joinTolerance           float64
	edgeMinLength           float64
	edgeMinLengthPrefilter  float64
	intersectionTolerance   float64
	textTolerance           float64
	minWordsVertical        int
	minWordsHorizontal      int
	explicitVerticalLines   string
	explicitHorizontalLines string
	indent                  int
}

// runExtract parses extract-subcommand args, opens the PDF, and
// dispatches to the requested output mode.
func runExtract(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var f extractFlags
	fs.StringVar(&f.pages, "pages", "", "Pages to extract: comma list (1,3) or dash range (1-5). Default: all.")
	fs.BoolVar(&f.tables, "tables", false, "Emit detected tables.")
	fs.BoolVar(&f.text, "text", false, "Emit extracted text (mutually exclusive with --tables).")
	fs.StringVar(&f.format, "format", "json", "Output format: json | text.")
	fs.StringVar(&f.verticalStrategy, "vertical-strategy", "lines", `Vertical edge strategy: lines | lines_strict | text | explicit.`)
	fs.StringVar(&f.horizontalStrategy, "horizontal-strategy", "lines", `Horizontal edge strategy: lines | lines_strict | text | explicit.`)
	fs.Float64Var(&f.snapTolerance, "snap-tolerance", 3, "Snap tolerance (PDF points).")
	fs.Float64Var(&f.joinTolerance, "join-tolerance", 3, "Join tolerance (PDF points).")
	fs.Float64Var(&f.edgeMinLength, "edge-min-length", 3, "Drop merged edges shorter than this (PDF points).")
	fs.Float64Var(&f.edgeMinLengthPrefilter, "edge-min-length-prefilter", 1, "Drop raw edges shorter than this before merging.")
	fs.Float64Var(&f.intersectionTolerance, "intersection-tolerance", 3, "Slack used when testing edge crossings (PDF points).")
	fs.Float64Var(&f.textTolerance, "text-tolerance", 3, "Per-cell text-extraction tolerance.")
	fs.IntVar(&f.minWordsVertical, "min-words-vertical", 3, "Min word count for text strategy vertical clusters.")
	fs.IntVar(&f.minWordsHorizontal, "min-words-horizontal", 1, "Min word count for text strategy horizontal clusters.")
	fs.StringVar(&f.explicitVerticalLines, "explicit-vertical-lines", "", "Comma-separated X coordinates for explicit strategy.")
	fs.StringVar(&f.explicitHorizontalLines, "explicit-horizontal-lines", "", "Comma-separated Y coordinates for explicit strategy.")
	fs.IntVar(&f.indent, "indent", 0, "JSON indent level. 0 = compact.")

	// Allow positional argument (path) before OR after flags by
	// shuffling args. Go's flag package stops at the first
	// non-flag; pdfplumber-style `extract file.pdf --tables` would
	// otherwise be rejected.
	reordered := reorderFlagsLast(args)
	if err := fs.Parse(reordered); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 1 {
		fs.Usage()
		return fmt.Errorf("missing input PDF path")
	}
	if len(positional) > 1 {
		return fmt.Errorf("unexpected positional arguments after %q: %v", positional[0], positional[1:])
	}
	path := positional[0]

	if f.tables && f.text {
		return fmt.Errorf("--tables and --text are mutually exclusive")
	}
	if !f.tables && !f.text {
		// Default to --tables when neither is specified. Mirrors the
		// pdfgrab library's primary use case.
		f.tables = true
	}
	if f.format != "json" && f.format != "text" {
		return fmt.Errorf("--format must be json or text, got %q", f.format)
	}

	doc, err := pdfgrab.OpenFile(path)
	if err != nil {
		return err
	}
	defer doc.Close()

	pageNums, err := parsePages(f.pages, doc.NumPages())
	if err != nil {
		return fmt.Errorf("--pages: %w", err)
	}

	settings, err := buildSettings(f)
	if err != nil {
		return err
	}

	if f.tables {
		return emitTables(doc, pageNums, settings, f, stdout)
	}
	return emitText(doc, pageNums, f, stdout)
}

// parsePages converts the --pages flag value into a sorted slice of
// 1-based page numbers. Empty string returns all pages.
//
// Examples:
//
//	""      → [1..N]
//	"1"     → [1]
//	"1,3"   → [1, 3]
//	"2-5"   → [2, 3, 4, 5]
//	"1,3-5" → [1, 3, 4, 5]
func parsePages(spec string, total int) ([]int, error) {
	if strings.TrimSpace(spec) == "" {
		out := make([]int, total)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}
	seen := make(map[int]struct{})
	out := make([]int, 0)
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if strings.Contains(tok, "-") {
			parts := strings.SplitN(tok, "-", 2)
			start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid range %q: %v", tok, err)
			}
			end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid range %q: %v", tok, err)
			}
			if start < 1 || end > total || start > end {
				return nil, fmt.Errorf("range %q out of bounds (1..%d)", tok, total)
			}
			for i := start; i <= end; i++ {
				if _, ok := seen[i]; ok {
					continue
				}
				seen[i] = struct{}{}
				out = append(out, i)
			}
		} else {
			n, err := strconv.Atoi(tok)
			if err != nil {
				return nil, fmt.Errorf("invalid page %q: %v", tok, err)
			}
			if n < 1 || n > total {
				return nil, fmt.Errorf("page %d out of bounds (1..%d)", n, total)
			}
			if _, ok := seen[n]; !ok {
				seen[n] = struct{}{}
				out = append(out, n)
			}
		}
	}
	return out, nil
}

// buildSettings translates the parsed flag set into a TableSettings.
// It also parses the explicit-line coordinate strings.
func buildSettings(f extractFlags) (pdfgrab.TableSettings, error) {
	s := pdfgrab.DefaultTableSettings()
	s.VerticalStrategy = pdfgrab.TableStrategy(f.verticalStrategy)
	s.HorizontalStrategy = pdfgrab.TableStrategy(f.horizontalStrategy)
	s.SnapTolerance = f.snapTolerance
	s.JoinTolerance = f.joinTolerance
	s.EdgeMinLength = f.edgeMinLength
	s.EdgeMinLengthPrefilter = f.edgeMinLengthPrefilter
	s.IntersectionTolerance = f.intersectionTolerance
	s.TextTolerance = f.textTolerance
	s.MinWordsVertical = f.minWordsVertical
	s.MinWordsHorizontal = f.minWordsHorizontal

	if f.explicitVerticalLines != "" {
		coords, err := parseFloatList(f.explicitVerticalLines)
		if err != nil {
			return s, fmt.Errorf("--explicit-vertical-lines: %w", err)
		}
		s.ExplicitVerticalLines = coords
	}
	if f.explicitHorizontalLines != "" {
		coords, err := parseFloatList(f.explicitHorizontalLines)
		if err != nil {
			return s, fmt.Errorf("--explicit-horizontal-lines: %w", err)
		}
		s.ExplicitHorizontalLines = coords
	}
	return s, nil
}

// reorderFlagsLast moves positional arguments to the end of the slice
// so the standard library flag package's "stop at first non-flag"
// behaviour doesn't get in the way of pdfplumber-style invocations
// like `pdfgrab extract file.pdf --tables`.
//
// Heuristic: a token is a flag if it starts with "-" or "--". Flags
// of the form "--foo=val" carry their value inline. Flags whose
// arguments are space-separated (e.g. "--pages 1-3") need to consume
// the following token; the boolean-flag set says which flags don't.
func reorderFlagsLast(args []string) []string {
	flagArgs := make([]string, 0, len(args))
	positional := make([]string, 0)
	i := 0
	for i < len(args) {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			// Inline value (--key=val): no follow-up token needed.
			if strings.Contains(a, "=") {
				i++
				continue
			}
			// Boolean flags don't consume the next token. Strip the
			// leading dashes to look up.
			name := strings.TrimLeft(a, "-")
			if _, isBool := boolFlagSet[name]; isBool {
				i++
				continue
			}
			// Otherwise, the next token (if any) is the value. Don't
			// promote a token that looks like another flag.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flagArgs = append(flagArgs, args[i+1])
				i += 2
				continue
			}
			i++
			continue
		}
		positional = append(positional, a)
		i++
	}
	return append(flagArgs, positional...)
}

// boolFlagSet lists every boolean flag the extract subcommand
// understands. Used by reorderFlagsLast to know which flags don't
// consume a following value token.
var boolFlagSet = map[string]struct{}{
	"tables": {},
	"text":   {},
	"h":      {},
	"help":   {},
}

// parseFloatList parses a comma-separated list of float coordinates.
func parseFloatList(spec string) ([]float64, error) {
	parts := strings.Split(spec, ",")
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float %q: %v", p, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// tablesOutput is the JSON shape emitted by `pdfgrab extract
// --tables`. We deliberately mirror pdfplumber's `to_json` schema
// where it overlaps: one entry per page, each carrying the page
// dimensions and a list of tables. Each table carries the row grid,
// the bbox, and the per-cell bboxes.
type tablesOutput struct {
	Pages []pageTablesOutput `json:"pages"`
}

type pageTablesOutput struct {
	Number int           `json:"number"`
	Width  float64       `json:"width"`
	Height float64       `json:"height"`
	Tables []tableOutput `json:"tables"`
}

type tableOutput struct {
	BBox  [4]float64     `json:"bbox"`
	Rows  [][]string     `json:"rows"`
	Cells [][][4]float64 `json:"cells"`
}

// emitTables runs ExtractTables on each requested page and writes the
// aggregated result in the requested format.
func emitTables(doc pdfgrab.Document, pages []int, settings pdfgrab.TableSettings, f extractFlags, w io.Writer) error {
	out := tablesOutput{Pages: make([]pageTablesOutput, 0, len(pages))}
	for _, n := range pages {
		page, err := doc.Page(n)
		if err != nil {
			return err
		}
		tables, err := page.ExtractTables(settings)
		if err != nil {
			return fmt.Errorf("page %d: %w", n, err)
		}
		pageOut := pageTablesOutput{
			Number: n,
			Width:  page.Width(),
			Height: page.Height(),
			Tables: make([]tableOutput, 0, len(tables)),
		}
		for _, t := range tables {
			to := tableOutput{
				BBox: [4]float64{t.BBox.X0, t.BBox.Y0, t.BBox.X1, t.BBox.Y1},
				Rows: t.Rows,
			}
			to.Cells = make([][][4]float64, len(t.CellsBBox))
			for ri, row := range t.CellsBBox {
				to.Cells[ri] = make([][4]float64, len(row))
				for ci, c := range row {
					to.Cells[ri][ci] = [4]float64{c.X0, c.Y0, c.X1, c.Y1}
				}
			}
			pageOut.Tables = append(pageOut.Tables, to)
		}
		out.Pages = append(out.Pages, pageOut)
	}

	if f.format == "text" {
		// One line per cell, blank line between rows, "---" between
		// tables, page-number header before each page.
		for _, p := range out.Pages {
			fmt.Fprintf(w, "=== Page %d (%g x %g) ===\n", p.Number, p.Width, p.Height)
			for ti, t := range p.Tables {
				if ti > 0 {
					fmt.Fprintln(w, "---")
				}
				for _, row := range t.Rows {
					fmt.Fprintln(w, strings.Join(row, "\t"))
				}
			}
		}
		return nil
	}

	enc := json.NewEncoder(w)
	if f.indent > 0 {
		enc.SetIndent("", strings.Repeat(" ", f.indent))
	}
	return enc.Encode(out)
}

// textOutput is the JSON shape for `pdfgrab extract --text`. One
// entry per page; text is the dense extract-text output.
type textOutput struct {
	Pages []pageTextOutput `json:"pages"`
}

type pageTextOutput struct {
	Number int     `json:"number"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Text   string  `json:"text"`
}

// emitText runs ExtractText on each requested page and writes the
// aggregated result in the requested format. --format text emits the
// text verbatim with a form-feed (\f) between pages, mirroring
// `pdftotext` and pdfplumber's --format text behaviour.
func emitText(doc pdfgrab.Document, pages []int, f extractFlags, w io.Writer) error {
	out := textOutput{Pages: make([]pageTextOutput, 0, len(pages))}
	for _, n := range pages {
		page, err := doc.Page(n)
		if err != nil {
			return err
		}
		text, err := page.ExtractText(pdfgrab.DefaultTextOpts())
		if err != nil {
			return fmt.Errorf("page %d: %w", n, err)
		}
		out.Pages = append(out.Pages, pageTextOutput{
			Number: n,
			Width:  page.Width(),
			Height: page.Height(),
			Text:   text,
		})
	}
	if f.format == "text" {
		for i, p := range out.Pages {
			if i > 0 {
				fmt.Fprint(w, "\f")
			}
			fmt.Fprintln(w, p.Text)
		}
		return nil
	}
	enc := json.NewEncoder(w)
	if f.indent > 0 {
		enc.SetIndent("", strings.Repeat(" ", f.indent))
	}
	return enc.Encode(out)
}
