// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"testing"

	"github.com/hallelx2/pdfgrab/internal/layout"
)

func vEdge(x, y0, y1 float64) layout.Edge {
	return layout.Edge{X0: x, X1: x, Y0: y0, Y1: y1, Orientation: layout.Vertical}
}

func hEdge(y, x0, x1 float64) layout.Edge {
	return layout.Edge{X0: x0, X1: x1, Y0: y, Y1: y, Orientation: layout.Horizontal}
}

// TestResolveAutoPicksPerAxis covers the decision table for StrategyAuto.
//
// The case it exists for is the table ruled on one axis only — booktabs
// style, the house style of most government and academic publishing.
// Such a table has no ruling intersections at all, so "lines" finds
// nothing on either axis.
func TestResolveAutoPicksPerAxis(t *testing.T) {
	fullGrid := []layout.Edge{
		vEdge(100, 0, 50), vEdge(200, 0, 50),
		hEdge(0, 100, 200), hEdge(50, 100, 200),
	}
	horizOnly := []layout.Edge{
		hEdge(0, 100, 200), hEdge(25, 100, 200), hEdge(50, 100, 200),
	}
	vertOnly := []layout.Edge{
		vEdge(100, 0, 50), vEdge(150, 0, 50), vEdge(200, 0, 50),
	}

	cases := []struct {
		name  string
		edges []layout.Edge
		wantV TableStrategy
		wantH TableStrategy
		why   string
	}{
		{
			"fully ruled", fullGrid, StrategyLines, StrategyLines,
			"both axes are ruled, so neither needs inferring",
		},
		{
			"horizontal rules only", horizOnly, StrategyText, StrategyLines,
			"rows come from the rules; columns must be inferred from words",
		},
		{
			"vertical rules only", vertOnly, StrategyLines, StrategyText,
			"the mirror image: columns from rules, rows inferred",
		},
		{
			"no rulings at all", nil, StrategyLines, StrategyLines,
			"NOT text/text — see below",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotV := resolveAuto(StrategyAuto, layout.Vertical, tc.edges)
			gotH := resolveAuto(StrategyAuto, layout.Horizontal, tc.edges)
			if gotV != tc.wantV || gotH != tc.wantH {
				t.Errorf("v=%q h=%q, want v=%q h=%q (%s)", gotV, gotH, tc.wantV, tc.wantH, tc.why)
			}
		})
	}
}

// TestResolveAutoDeclinesWithoutEvidence is the property that keeps
// StrategyAuto from wrecking precision.
//
// Falling back to "text" on a page with no rulings is what makes a naive
// lines->text fallback score WORSE than lines alone: measured on ICDAR
// 2013 it drops precision from 0.865 to 0.223, because prose has word
// alignment too and the text strategy will report a table for it.
//
// Rulings on the OTHER axis are the evidence that a table is really
// present. Without that evidence Auto must decline to guess.
func TestResolveAutoDeclinesWithoutEvidence(t *testing.T) {
	// A prose page: no rulings anywhere.
	if got := resolveAuto(StrategyAuto, layout.Vertical, nil); got != StrategyLines {
		t.Errorf("no rulings resolved to %q; must stay %q so prose is not read as a table",
			got, StrategyLines)
	}

	// A single stray rule — an underline, a header separator, a page
	// border — is not a table and must not count as evidence.
	one := []layout.Edge{hEdge(0, 100, 200)}
	if got := resolveAuto(StrategyAuto, layout.Vertical, one); got != StrategyLines {
		t.Errorf("one stray rule resolved to %q; %d edges are needed before an axis counts as ruled",
			got, minEdgesForAxis)
	}
}

// TestResolveAutoLeavesOtherStrategiesAlone guards the pass-through.
func TestResolveAutoLeavesOtherStrategiesAlone(t *testing.T) {
	for _, s := range []TableStrategy{
		StrategyLines, StrategyLinesStrict, StrategyText, StrategyExplicit,
	} {
		if got := resolveAuto(s, layout.Vertical, nil); got != s {
			t.Errorf("resolveAuto(%q) = %q, want it untouched", s, got)
		}
	}
}

// TestAutoIsNotTheDefault pins that opting in is required. Auto improves
// table DETECTION but measurably lowers aggregate F1 on ICDAR 2013
// (0.362 -> 0.358), so it must never be silently switched on.
func TestAutoIsNotTheDefault(t *testing.T) {
	d := DefaultTableSettings()
	if d.VerticalStrategy == StrategyAuto || d.HorizontalStrategy == StrategyAuto {
		t.Error("StrategyAuto must not be a default — it lowers aggregate benchmark F1")
	}
}
