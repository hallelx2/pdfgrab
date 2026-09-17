// Emit every table pdfgrab finds in a PDF, as JSON, for benchmarking.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/hallelx2/pdfgrab"
)

type tableOut struct {
	Page int        `json:"page"`
	Rows [][]string `json:"rows"`
}

// edgeSet is one page's explicit row/column boundaries, in PDF points.
type edgeSet struct {
	V []float64 `json:"v"`
	H []float64 `json:"h"`
}

func main() {
	strategy := flag.String("strategy", "lines",
		"lines | text | mixed | auto | lines-then-mixed | fallback")
	merge := flag.Bool("merge", false, "TableSettings.MergeSplitTokens")
	detect := flag.Bool("detect", false, "TableSettings.DetectRegions (text-alignment region detection)")
	minwv := flag.Int("minwv", 0, "TableSettings.MinWordsVertical (0 = leave default)")
	minwh := flag.Int("minwh", 0, "TableSettings.MinWordsHorizontal (0 = leave default)")
	pad := flag.Float64("pad", 0, "TextEdge.PadLines in average line heights (0 = default, negative = none)")
	oracle := flag.String("oracle", "",
		`JSON of per-page explicit edges: {"1":{"v":[..],"h":[..]}}`)
	flag.Parse()

	doc, err := pdfgrab.OpenFile(flag.Arg(0))
	if err != nil {
		// A file we cannot open scores zero rather than aborting the run;
		// the harness needs a result for every document.
		json.NewEncoder(os.Stdout).Encode([]tableOut{})
		fmt.Fprintln(os.Stderr, "open:", err)
		return
	}
	defer doc.Close()

	mk := func(v, h pdfgrab.TableStrategy) pdfgrab.TableSettings {
		s := pdfgrab.DefaultTableSettings()
		s.VerticalStrategy, s.HorizontalStrategy = v, h
		s.MergeSplitTokens = *merge
		return s
	}
	lines := mk(pdfgrab.StrategyLines, pdfgrab.StrategyLines)
	text := mk(pdfgrab.StrategyText, pdfgrab.StrategyText)
	// "mixed" is the booktabs case: horizontal rules give the rows, word
	// alignment gives the columns.
	mixed := mk(pdfgrab.StrategyText, pdfgrab.StrategyLines)
	auto := mk(pdfgrab.StrategyAuto, pdfgrab.StrategyAuto)

	var attempts []pdfgrab.TableSettings
	switch *strategy {
	case "text":
		attempts = []pdfgrab.TableSettings{text}
	case "mixed":
		attempts = []pdfgrab.TableSettings{mixed}
	case "auto":
		attempts = []pdfgrab.TableSettings{auto}
	case "lines-then-mixed":
		attempts = []pdfgrab.TableSettings{lines, mixed}
	case "fallback":
		attempts = []pdfgrab.TableSettings{lines, text}
	default:
		attempts = []pdfgrab.TableSettings{lines}
	}

	// Region detection is a property of the run, not of a strategy, so
	// it is applied to whichever attempts the strategy selected. It only
	// affects text-derived axes; on a pure "lines" run it is inert.
	if *detect {
		for i := range attempts {
			attempts[i].DetectRegions = true
			attempts[i].TextEdge.PadLines = *pad
		}
	}

	// Threshold overrides for the HAL-1363 sweep. Zero leaves whatever
	// the strategy preset chose, so an unswept run is unaffected.
	for i := range attempts {
		if *minwv > 0 {
			attempts[i].MinWordsVertical = *minwv
		}
		if *minwh > 0 {
			attempts[i].MinWordsHorizontal = *minwh
		}
	}

	// Oracle mode: the caller supplies the row/column boundaries and
	// pdfgrab only fills the cells. This is exactly the shape of the
	// hybrid a layout model would drive — and fed GROUND-TRUTH edges it
	// measures the ceiling that hybrid can reach: how good extraction gets
	// if detection and gridding were solved perfectly.
	var oracleEdges map[string]edgeSet
	if *oracle != "" {
		if b, err := os.ReadFile(*oracle); err == nil {
			_ = json.Unmarshal(b, &oracleEdges)
		}
	}

	out := []tableOut{}
	for i := 1; i <= doc.NumPages(); i++ {
		p, err := doc.Page(i)
		if err != nil {
			continue
		}

		if oracleEdges != nil {
			e, ok := oracleEdges[strconv.Itoa(i)]
			if !ok || len(e.V) < 2 || len(e.H) < 2 {
				continue
			}
			s := mk(pdfgrab.StrategyExplicit, pdfgrab.StrategyExplicit)
			s.ExplicitVerticalLines = e.V
			s.ExplicitHorizontalLines = e.H
			if tables, err := p.ExtractTables(s); err == nil {
				for _, t := range tables {
					out = append(out, tableOut{Page: i, Rows: t.Rows})
				}
			}
			continue
		}

		for _, s := range attempts {
			tables, err := p.ExtractTables(s)
			if err != nil || len(tables) == 0 {
				continue
			}
			for _, t := range tables {
				out = append(out, tableOut{Page: i, Rows: t.Rows})
			}
			break
		}
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
