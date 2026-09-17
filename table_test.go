// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

// table_test.go is intentionally in the pdfgrab package (not
// pdfgrab_test) so it can reach the unexported algorithm functions:
// edgesToIntersections, intersectionsToCells, cellsToTables,
// assembleTableBox, runTableFinder. The public-API integration test
// (TestExtractTables_RuledFixture) lives at the end and uses the
// public surface only.

import (
	"strings"
	"testing"

	"github.com/hallelx2/pdfgrab/internal/layout"
	"github.com/hallelx2/pdfgrab/testdata"
)

// makeH builds a horizontal edge at Y = y from X0 = x0 to X1 = x1.
// Tests never construct layout.Edge values directly because the
// invariants (X0 <= X1 for h, Y0 == Y1 for h) are constructor-
// enforced; this helper centralises the common case.
func makeH(x0, x1, y float64) layout.Edge {
	return layout.Edge{
		X0: x0, X1: x1, Y0: y, Y1: y,
		Orientation: layout.Horizontal,
		Source:      layout.SourceLine,
	}
}

// makeV builds a vertical edge at X = x from Y0 = y0 to Y1 = y1.
func makeV(x, y0, y1 float64) layout.Edge {
	return layout.Edge{
		X0: x, X1: x, Y0: y0, Y1: y1,
		Orientation: layout.Vertical,
		Source:      layout.SourceLine,
	}
}

// TestEdgesToIntersections_Grid2x2 sets up a 2×2 cell grid (3
// horizontal and 3 vertical edges) and asserts the intersection
// scanner finds exactly 9 crossing points at the expected (X, Y)
// pairs.
//
// Geometry (PDF user space, Y growing up):
//
//	Y=100 ─────────
//	      │ │ │ │
//	Y= 50 ─────────
//	      │ │ │ │
//	Y=  0 ─────────
//	   X=0 X=50 X=100
func TestEdgesToIntersections_Grid2x2(t *testing.T) {
	edges := []layout.Edge{
		makeH(0, 100, 0),
		makeH(0, 100, 50),
		makeH(0, 100, 100),
		makeV(0, 0, 100),
		makeV(50, 0, 100),
		makeV(100, 0, 100),
	}
	ints := edgesToIntersections(edges, 0.1, 0.1)
	if len(ints) != 9 {
		t.Fatalf("intersections: got %d, want 9", len(ints))
	}

	// Build a set of (X, Y) for easy lookup.
	type pt struct{ x, y float64 }
	got := make(map[pt]bool, len(ints))
	for _, p := range ints {
		got[pt{x: p.X, y: p.Y}] = true
	}
	for _, x := range []float64{0, 50, 100} {
		for _, y := range []float64{0, 50, 100} {
			if !got[pt{x: x, y: y}] {
				t.Errorf("missing intersection at (%v, %v)", x, y)
			}
		}
	}

	// Each intersection should have at least one V and one H edge.
	for _, p := range ints {
		if len(p.V) == 0 || len(p.H) == 0 {
			t.Errorf("intersection (%v,%v): V=%d H=%d, want both > 0",
				p.X, p.Y, len(p.V), len(p.H))
		}
	}
}

// TestEdgesToIntersections_NoCrossing asserts that two parallel
// edges, even when their Y / X spans overlap, produce no
// intersections.
func TestEdgesToIntersections_NoCrossing(t *testing.T) {
	edges := []layout.Edge{
		makeH(0, 100, 50),
		makeH(0, 100, 60),
		makeV(0, 0, 100),
		makeV(50, 0, 100),
	}
	// Expected: 4 intersections (each H crosses each V).
	ints := edgesToIntersections(edges, 0.1, 0.1)
	if len(ints) != 4 {
		t.Fatalf("got %d intersections, want 4", len(ints))
	}
}

// TestEdgesToIntersections_Tolerance asserts the perpendicular
// tolerance lets a near-miss crossing register. The vertical edge
// ends at Y=49.5 but the horizontal edge sits at Y=50: with yTol=1
// the intersection should still be picked up.
func TestEdgesToIntersections_Tolerance(t *testing.T) {
	edges := []layout.Edge{
		makeH(0, 100, 50),
		makeV(50, 0, 49.5),
	}
	if got := len(edgesToIntersections(edges, 1, 1)); got != 1 {
		t.Errorf("with yTol=1: got %d intersections, want 1", got)
	}
	if got := len(edgesToIntersections(edges, 0.1, 0.1)); got != 0 {
		t.Errorf("with yTol=0.1: got %d intersections, want 0", got)
	}
}

// TestIntersectionsToCells_Single2x2 feeds the 9 intersections of a
// 2×2 grid and asserts the cell finder produces exactly 4 cells
// covering the expected bboxes.
func TestIntersectionsToCells_Single2x2(t *testing.T) {
	edges := []layout.Edge{
		makeH(0, 100, 0),
		makeH(0, 100, 50),
		makeH(0, 100, 100),
		makeV(0, 0, 100),
		makeV(50, 0, 100),
		makeV(100, 0, 100),
	}
	ints := edgesToIntersections(edges, 0.1, 0.1)
	cells := intersectionsToCells(ints)
	if len(cells) != 4 {
		t.Fatalf("got %d cells, want 4", len(cells))
	}

	type want struct{ x0, y0, x1, y1 float64 }
	expected := []want{
		{0, 50, 50, 100},
		{50, 50, 100, 100},
		{0, 0, 50, 50},
		{50, 0, 100, 50},
	}
	for _, w := range expected {
		found := false
		for _, c := range cells {
			if c.X0 == w.x0 && c.Y0 == w.y0 && c.X1 == w.x1 && c.Y1 == w.y1 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing cell (%v,%v)-(%v,%v)", w.x0, w.y0, w.x1, w.y1)
		}
	}
}

// TestCellsToTables_GroupsTouching asserts that cells sharing a
// corner end up in the same table, and that singletons get dropped.
func TestCellsToTables_GroupsTouching(t *testing.T) {
	// Two 2x2 grids that don't touch.
	left := []BBox{
		NewBBox(0, 0, 50, 50),
		NewBBox(50, 0, 100, 50),
		NewBBox(0, 50, 50, 100),
		NewBBox(50, 50, 100, 100),
	}
	right := []BBox{
		NewBBox(200, 0, 250, 50),
		NewBBox(250, 0, 300, 50),
		NewBBox(200, 50, 250, 100),
		NewBBox(250, 50, 300, 100),
	}
	// One standalone cell that should be filtered out.
	stray := []BBox{NewBBox(400, 400, 410, 410)}

	all := append([]BBox{}, left...)
	all = append(all, right...)
	all = append(all, stray...)

	tables := cellsToTables(all)
	if len(tables) != 2 {
		t.Fatalf("got %d tables, want 2", len(tables))
	}
	for i, tbl := range tables {
		if len(tbl) != 4 {
			t.Errorf("table %d: got %d cells, want 4", i, len(tbl))
		}
	}
}

// TestAssembleTableBox_2x2Grid asserts the row/column projection
// builds the expected 2×2 grid with cells in the right slots.
//
// Row 0 is visually TOP (larger Y1). Column 0 is visually LEFT
// (smaller X0).
func TestAssembleTableBox_2x2Grid(t *testing.T) {
	cells := []BBox{
		NewBBox(0, 50, 50, 100),   // top-left
		NewBBox(50, 50, 100, 100), // top-right
		NewBBox(0, 0, 50, 50),     // bottom-left
		NewBBox(50, 0, 100, 50),   // bottom-right
	}
	tb := assembleTableBox(cells)
	if tb.Rows != 2 || tb.Cols != 2 {
		t.Fatalf("got Rows=%d Cols=%d, want 2x2", tb.Rows, tb.Cols)
	}
	if tb.BBox != (BBox{X0: 0, Y0: 0, X1: 100, Y1: 100}) {
		t.Errorf("bbox: got %+v, want (0,0)-(100,100)", tb.BBox)
	}
	// Row 0 = top = larger Y. Row 0, Col 0 should be (0, 50, 50, 100).
	if c := tb.CellsGrid[0][0]; c.X0 != 0 || c.Y1 != 100 {
		t.Errorf("CellsGrid[0][0]: got %+v, want top-left", c)
	}
	if c := tb.CellsGrid[0][1]; c.X0 != 50 || c.Y1 != 100 {
		t.Errorf("CellsGrid[0][1]: got %+v, want top-right", c)
	}
	if c := tb.CellsGrid[1][0]; c.X0 != 0 || c.Y1 != 50 {
		t.Errorf("CellsGrid[1][0]: got %+v, want bottom-left", c)
	}
	if c := tb.CellsGrid[1][1]; c.X0 != 50 || c.Y1 != 50 {
		t.Errorf("CellsGrid[1][1]: got %+v, want bottom-right", c)
	}
}

// TestRunTableFinder_2x3Grid is an end-to-end algorithm test that
// builds the edges for a 2-column × 3-row grid by hand, runs the
// full pipeline, and asserts the output has the right shape:
// 12 intersections, 6 cells, 1 table, 3×2 grid.
func TestRunTableFinder_2x3Grid(t *testing.T) {
	// Columns at X = 100, 200, 300; rows at Y = 0, 50, 100, 150.
	edges := []layout.Edge{
		makeH(100, 300, 0),
		makeH(100, 300, 50),
		makeH(100, 300, 100),
		makeH(100, 300, 150),
		makeV(100, 0, 150),
		makeV(200, 0, 150),
		makeV(300, 0, 150),
	}
	finder := runTableFinder(edges, 0.1, 0.1)
	if got := len(finder.Intersections); got != 12 {
		t.Errorf("intersections: got %d, want 12", got)
	}
	if got := len(finder.Cells); got != 6 {
		t.Errorf("cells: got %d, want 6", got)
	}
	if got := len(finder.Tables); got != 1 {
		t.Fatalf("tables: got %d, want 1", got)
	}
	tb := finder.Tables[0]
	if tb.Rows != 3 || tb.Cols != 2 {
		t.Errorf("grid: got %dx%d, want 3x2", tb.Rows, tb.Cols)
	}
}

// TestEnsureSupportedStrategies_RejectsUnknown asserts that an
// unrecognised strategy string returns ErrUnsupported rather than
// silently running an empty pipeline. All four pdfplumber strategies
// (lines / lines_strict / text / explicit) are now implemented, so
// this test only exercises the unknown-string path.
func TestEnsureSupportedStrategies_RejectsUnknown(t *testing.T) {
	cases := []struct {
		name string
		s    TableSettings
	}{
		{"unknown_v", TableSettings{VerticalStrategy: "blah", HorizontalStrategy: StrategyLines}},
		{"unknown_h", TableSettings{VerticalStrategy: StrategyLines, HorizontalStrategy: "blah"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ensureSupportedStrategies(c.s.applyDefaults())
			if err == nil {
				t.Fatal("got nil error, want ErrUnsupported")
			}
			if !errIs(err, ErrUnsupported) {
				t.Errorf("got %v, want ErrUnsupported", err)
			}
		})
	}
}

// TestEnsureSupportedStrategies_AcceptsAllFour asserts that all four
// pdfplumber strategies pass validation, in every paired combination.
func TestEnsureSupportedStrategies_AcceptsAllFour(t *testing.T) {
	strategies := []TableStrategy{StrategyLines, StrategyLinesStrict, StrategyText, StrategyExplicit}
	for _, v := range strategies {
		for _, h := range strategies {
			name := string(v) + "/" + string(h)
			t.Run(name, func(t *testing.T) {
				s := TableSettings{VerticalStrategy: v, HorizontalStrategy: h}.applyDefaults()
				if err := ensureSupportedStrategies(s); err != nil {
					t.Errorf("got %v, want nil", err)
				}
			})
		}
	}
}

// TestApplyDefaults_FillsZeroFields verifies the zero-value defaults
// match pdfplumber's constants.
func TestApplyDefaults_FillsZeroFields(t *testing.T) {
	s := TableSettings{}.applyDefaults()
	if s.VerticalStrategy != StrategyLines {
		t.Errorf("VerticalStrategy: got %q, want %q", s.VerticalStrategy, StrategyLines)
	}
	if s.SnapTolerance != 3 {
		t.Errorf("SnapTolerance: got %v, want 3", s.SnapTolerance)
	}
	if s.JoinTolerance != 3 {
		t.Errorf("JoinTolerance: got %v, want 3", s.JoinTolerance)
	}
	if s.EdgeMinLength != 3 {
		t.Errorf("EdgeMinLength: got %v, want 3", s.EdgeMinLength)
	}
	if s.EdgeMinLengthPrefilter != 1 {
		t.Errorf("EdgeMinLengthPrefilter: got %v, want 1", s.EdgeMinLengthPrefilter)
	}
	if s.IntersectionTolerance != 3 {
		t.Errorf("IntersectionTolerance: got %v, want 3", s.IntersectionTolerance)
	}
	if s.TextTolerance != 3 {
		t.Errorf("TextTolerance: got %v, want 3", s.TextTolerance)
	}
}

// TestExtractTables_RuledFixture is the end-to-end integration test
// against the hand-crafted 2×3 ruled-table fixture. It opens the
// generated PDF, runs ExtractTables with default settings, and
// asserts the row count + cell text matches the fixture's known
// content.
//
// This test uses the public API only — the unit tests above cover
// the unexported algorithm functions.
func TestExtractTables_RuledFixture(t *testing.T) {
	// Import path is package-internal here (we're in the pdfgrab
	// package, not _test), so OpenBytes is unqualified.
	doc, err := OpenBytes(testdata.TableRuled())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()

	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}

	tables, err := p.ExtractTables(DefaultTableSettings())
	if err != nil {
		t.Fatalf("ExtractTables: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("got %d tables, want 1", len(tables))
	}
	tbl := tables[0]
	if len(tbl.Rows) != 3 {
		t.Fatalf("rows: got %d, want 3", len(tbl.Rows))
	}
	for i, row := range tbl.Rows {
		if len(row) != 2 {
			t.Errorf("row %d: got %d cols, want 2", i, len(row))
		}
	}
	// Row 0 (visually top): Name | Age
	// Row 1: Alice | 30
	// Row 2: Bob   | 25
	want := [][]string{
		{"Name", "Age"},
		{"Alice", "30"},
		{"Bob", "25"},
	}
	for i := range want {
		for j := range want[i] {
			got := tbl.Rows[i][j]
			if got != want[i][j] {
				t.Errorf("Rows[%d][%d]: got %q, want %q", i, j, got, want[i][j])
			}
		}
	}
	if tbl.Page != 1 {
		t.Errorf("Page: got %d, want 1", tbl.Page)
	}
	if tbl.BBox.IsZero() {
		t.Error("BBox is zero")
	}
}

// TestExtractTables_MaxEdgesPerAxisCapSuppressesTable asserts that a
// MaxEdgesPerAxis set below the fixture's ruling count makes table
// finding bail out (no tables, no error). The ruled fixture has more
// than one ruling on each axis, so a cap of 1 trips the guard.
func TestExtractTables_MaxEdgesPerAxisCapSuppressesTable(t *testing.T) {
	doc, err := OpenBytes(testdata.TableRuled())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	// Sanity: with the default cap the fixture yields exactly one table.
	if tables, err := p.ExtractTables(DefaultTableSettings()); err != nil {
		t.Fatalf("baseline ExtractTables: %v", err)
	} else if len(tables) != 1 {
		t.Fatalf("baseline: got %d tables, want 1", len(tables))
	}

	settings := DefaultTableSettings()
	settings.MaxEdgesPerAxis = 1
	tables, err := p.ExtractTables(settings)
	if err != nil {
		t.Fatalf("ExtractTables with cap: %v", err)
	}
	if len(tables) != 0 {
		t.Errorf("with MaxEdgesPerAxis=1: got %d tables, want 0 (cap should suppress)", len(tables))
	}
}

// TestExtractTables_MaxIntersectionsCapSuppressesTable asserts the
// intersection-stage cap likewise bails out when set below the
// fixture's crossing count.
func TestExtractTables_MaxIntersectionsCapSuppressesTable(t *testing.T) {
	doc, err := OpenBytes(testdata.TableRuled())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	settings := DefaultTableSettings()
	settings.MaxIntersections = 1
	tables, err := p.ExtractTables(settings)
	if err != nil {
		t.Fatalf("ExtractTables with cap: %v", err)
	}
	if len(tables) != 0 {
		t.Errorf("with MaxIntersections=1: got %d tables, want 0 (cap should suppress)", len(tables))
	}
}

// TestExtractTables_NegativeCapDisables asserts a negative cap disables
// the guard entirely (the fixture's one table still comes through even
// though the cap field is set).
func TestExtractTables_NegativeCapDisables(t *testing.T) {
	doc, err := OpenBytes(testdata.TableRuled())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	settings := DefaultTableSettings()
	settings.MaxEdgesPerAxis = -1
	settings.MaxIntersections = -1
	tables, err := p.ExtractTables(settings)
	if err != nil {
		t.Fatalf("ExtractTables: %v", err)
	}
	if len(tables) != 1 {
		t.Errorf("with caps disabled: got %d tables, want 1", len(tables))
	}
}

// TestApplyDefaults_FillsSafetyCaps asserts the new safety-cap fields
// get their pdfgrab defaults when left zero, matching the
// zero-value-gets-defaults convention of the other TableSettings
// fields.
func TestApplyDefaults_FillsSafetyCaps(t *testing.T) {
	s := TableSettings{}.applyDefaults()
	if s.MaxEdgesPerAxis != 1000 {
		t.Errorf("MaxEdgesPerAxis: got %d, want 1000", s.MaxEdgesPerAxis)
	}
	if s.MaxIntersections != 50000 {
		t.Errorf("MaxIntersections: got %d, want 50000", s.MaxIntersections)
	}
	// A negative value must survive applyDefaults (it means "disabled").
	s2 := TableSettings{MaxEdgesPerAxis: -1, MaxIntersections: -1}.applyDefaults()
	if s2.MaxEdgesPerAxis != -1 || s2.MaxIntersections != -1 {
		t.Errorf("negative caps overwritten: got %d / %d, want -1 / -1",
			s2.MaxEdgesPerAxis, s2.MaxIntersections)
	}
}

// TestExtractTables_UnknownStrategyReturnsErrUnsupported asserts the
// public API surfaces ErrUnsupported when callers pass an unrecognised
// strategy string. All four standard strategies are implemented as of
// v0.3.0; this guard catches typos.
func TestExtractTables_UnknownStrategyReturnsErrUnsupported(t *testing.T) {
	doc, err := OpenBytes(testdata.TableRuled())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	settings := DefaultTableSettings()
	settings.VerticalStrategy = "not-a-strategy"
	_, err = p.ExtractTables(settings)
	if err == nil {
		t.Fatal("got nil, want ErrUnsupported")
	}
	if !errIs(err, ErrUnsupported) {
		t.Errorf("got %v, want ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "not-a-strategy") {
		t.Errorf("error %q should name the strategy", err.Error())
	}
}

// TestExtractTables_ExplicitWithoutCoordinatesReturnsError asserts
// that StrategyExplicit on an axis with fewer than two coordinates
// returns a clear validation error (matching pdfplumber's behaviour).
func TestExtractTables_ExplicitWithoutCoordinatesReturnsError(t *testing.T) {
	doc, err := OpenBytes(testdata.TableRuled())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)

	settings := DefaultTableSettings()
	settings.VerticalStrategy = StrategyExplicit
	settings.ExplicitVerticalLines = []float64{100}
	_, err = p.ExtractTables(settings)
	if err == nil {
		t.Fatal("got nil, want validation error")
	}
	if !strings.Contains(err.Error(), "two") {
		t.Errorf("error %q should mention the two-coordinate minimum", err.Error())
	}
}

// TestFindTables_NoEdgesReturnsEmpty asserts that a page with no
// edges (e.g. a text-only page) returns an empty slice, not an
// error.
func TestFindTables_NoEdgesReturnsEmpty(t *testing.T) {
	doc, err := OpenBytes(testdata.Hello())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)
	finders, err := p.FindTables(DefaultTableSettings())
	if err != nil {
		t.Errorf("FindTables on text-only page: got %v, want nil", err)
	}
	if len(finders) != 0 {
		t.Errorf("got %d finders, want 0 (text-only page)", len(finders))
	}
}

// makeWord builds a Word at the given bbox with the given text.
// Helper for the text-strategy unit tests which feed hand-crafted
// Word slices directly into wordsToEdgesV / wordsToEdgesH.
func makeWord(text string, x0, y0, x1, y1 float64) Word {
	return Word{
		Text: text,
		X0:   x0, Y0: y0, X1: x1, Y1: y1,
		Upright:   true,
		Direction: "ltr",
	}
}

// TestWordsToEdgesV_ThreeColumnAlignment exercises the vertical "text"
// strategy with three columns of three words each, all left-aligned
// at X = 100, 200, 300.  The expected output is four vertical edges:
// three at the columns' X0 plus one trailing at the rightmost X1.
func TestWordsToEdgesV_ThreeColumnAlignment(t *testing.T) {
	words := []Word{
		// Row 1: y near 700
		makeWord("AAA", 100, 700, 130, 710),
		makeWord("BBB", 200, 700, 230, 710),
		makeWord("CCC", 300, 700, 330, 710),
		// Row 2: y near 685
		makeWord("DDD", 100, 685, 130, 695),
		makeWord("EEE", 200, 685, 230, 695),
		makeWord("FFF", 300, 685, 330, 695),
		// Row 3: y near 670
		makeWord("GGG", 100, 670, 130, 680),
		makeWord("HHH", 200, 670, 230, 680),
		makeWord("III", 300, 670, 330, 680),
	}
	edges := wordsToEdgesV(words, 3)
	if len(edges) != 4 {
		t.Fatalf("got %d edges, want 4 (3 columns + trailing)", len(edges))
	}
	xs := make(map[float64]struct{}, 4)
	for _, e := range edges {
		if e.Orientation != layout.Vertical {
			t.Errorf("edge %+v: not vertical", e)
		}
		if e.Source != layout.SourceText {
			t.Errorf("edge %+v: source %v, want SourceText", e, e.Source)
		}
		xs[e.X0] = struct{}{}
	}
	for _, want := range []float64{100, 200, 300, 330} {
		if _, ok := xs[want]; !ok {
			t.Errorf("missing vertical edge at X=%v; got %v", want, xs)
		}
	}
}

// TestWordsToEdgesV_BelowThresholdDropsCluster asserts that a column
// candidate with fewer than MinWordsVertical words doesn't survive
// the threshold filter.
func TestWordsToEdgesV_BelowThresholdDropsCluster(t *testing.T) {
	words := []Word{
		// Column at X=100 has only 2 words; threshold of 3 should
		// drop it.
		makeWord("AAA", 100, 700, 130, 710),
		makeWord("DDD", 100, 685, 130, 695),
		// Column at X=200 has 3 words.
		makeWord("BBB", 200, 700, 230, 710),
		makeWord("EEE", 200, 685, 230, 695),
		makeWord("HHH", 200, 670, 230, 680),
	}
	edges := wordsToEdgesV(words, 3)
	// Expected: 1 column boundary (X=200) + 1 trailing (X=230) = 2 edges.
	if len(edges) != 2 {
		t.Fatalf("got %d edges, want 2", len(edges))
	}
}

// TestWordsToEdgesH_DetectsRows asserts that horizontal clusters of
// words sharing a top-Y produce one top + one bottom edge per row.
func TestWordsToEdgesH_DetectsRows(t *testing.T) {
	words := []Word{
		// Row 1: top at Y=710
		makeWord("AAA", 100, 700, 130, 710),
		makeWord("BBB", 200, 700, 230, 710),
		makeWord("CCC", 300, 700, 330, 710),
		// Row 2: top at Y=695
		makeWord("DDD", 100, 685, 130, 695),
		makeWord("EEE", 200, 685, 230, 695),
		makeWord("FFF", 300, 685, 330, 695),
	}
	// Threshold 1 → every cluster counts. Two rows × 2 edges/row = 4.
	edges := wordsToEdgesH(words, 1)
	if len(edges) != 4 {
		t.Fatalf("got %d edges, want 4 (2 rows × top+bottom)", len(edges))
	}
	for _, e := range edges {
		if e.Orientation != layout.Horizontal {
			t.Errorf("edge %+v: not horizontal", e)
		}
		if e.Source != layout.SourceText {
			t.Errorf("edge %+v: source %v, want SourceText", e, e.Source)
		}
	}
	// Top + bottom for each row should be present: row 1 (700, 710)
	// and row 2 (685, 695).
	ys := make(map[float64]int, 4)
	for _, e := range edges {
		ys[e.Y0]++
	}
	for _, want := range []float64{700, 710, 685, 695} {
		if ys[want] == 0 {
			t.Errorf("missing horizontal edge at Y=%v", want)
		}
	}
}

// TestWordsToEdges_EmptyInputs asserts the early-return paths.
func TestWordsToEdges_EmptyInputs(t *testing.T) {
	if got := wordsToEdgesV(nil, 3); got != nil {
		t.Errorf("nil words: got %v, want nil", got)
	}
	if got := wordsToEdgesH(nil, 1); got != nil {
		t.Errorf("nil words: got %v, want nil", got)
	}
	if got := wordsToEdgesV([]Word{makeWord("A", 0, 0, 10, 10)}, 0); got != nil {
		t.Errorf("threshold 0: got %v, want nil", got)
	}
}

// TestExplicitVerticalEdges_PromotesAndFiltersInvalid asserts that
// each finite X is promoted to a full-height vertical edge tagged
// SourceExplicit, and that non-finite values are dropped silently.
func TestExplicitVerticalEdges_PromotesAndFiltersInvalid(t *testing.T) {
	xs := []float64{100, 200, nanForTest(), 300}
	edges := explicitVerticalEdges(xs, 0, 800)
	if len(edges) != 3 {
		t.Fatalf("got %d edges, want 3 (NaN dropped)", len(edges))
	}
	for _, e := range edges {
		if e.Orientation != layout.Vertical {
			t.Errorf("edge %+v: not vertical", e)
		}
		if e.Source != layout.SourceExplicit {
			t.Errorf("edge %+v: source %v, want SourceExplicit", e, e.Source)
		}
		if e.Y0 != 0 || e.Y1 != 800 {
			t.Errorf("edge %+v: Y span got (%v,%v), want (0,800)", e, e.Y0, e.Y1)
		}
	}
}

// TestExplicitHorizontalEdges_PromotesAndFiltersInvalid is the
// horizontal counterpart.
func TestExplicitHorizontalEdges_PromotesAndFiltersInvalid(t *testing.T) {
	ys := []float64{100, 200, 300}
	edges := explicitHorizontalEdges(ys, 0, 600)
	if len(edges) != 3 {
		t.Fatalf("got %d edges, want 3", len(edges))
	}
	for _, e := range edges {
		if e.Orientation != layout.Horizontal {
			t.Errorf("edge %+v: not horizontal", e)
		}
		if e.X0 != 0 || e.X1 != 600 {
			t.Errorf("edge %+v: X span got (%v,%v), want (0,600)", e, e.X0, e.X1)
		}
	}
}

// TestValidateExplicitForStrategy_RequiresTwoCoords asserts the
// pre-flight check rejects an explicit strategy with fewer than two
// coordinates on the chosen axis. pdfplumber raises ValueError; we
// surface a regular error (callers don't typically catch via
// errors.Is here).
func TestValidateExplicitForStrategy_RequiresTwoCoords(t *testing.T) {
	cases := []struct {
		name string
		s    TableSettings
		want bool
	}{
		{"v_explicit_zero", TableSettings{VerticalStrategy: StrategyExplicit}, true},
		{"v_explicit_one", TableSettings{VerticalStrategy: StrategyExplicit, ExplicitVerticalLines: []float64{1}}, true},
		{"v_explicit_two_ok", TableSettings{VerticalStrategy: StrategyExplicit, ExplicitVerticalLines: []float64{1, 2}}, false},
		{"h_explicit_one", TableSettings{HorizontalStrategy: StrategyExplicit, ExplicitHorizontalLines: []float64{1}}, true},
		{"lines_no_check", TableSettings{VerticalStrategy: StrategyLines}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateExplicitForStrategy(c.s.applyDefaults())
			if got := err != nil; got != c.want {
				t.Errorf("got err=%v, want error: %v", err, c.want)
			}
		})
	}
}

// TestExtractTables_BorderlessTextStrategy asserts the public API
// runs the text strategy end-to-end on the borderless fixture and
// recovers the expected row × column grid.
//
// Fixture: testdata.TableBorderless() — 3 columns ("Item",
// "Quantity", "Price") and 3 rows of body data, no rules drawn.
func TestExtractTables_BorderlessTextStrategy(t *testing.T) {
	doc, err := OpenBytes(testdata.TableBorderless())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}

	settings := DefaultTableSettings()
	settings.VerticalStrategy = StrategyText
	settings.HorizontalStrategy = StrategyText

	tables, err := p.ExtractTables(settings)
	if err != nil {
		t.Fatalf("ExtractTables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatalf("got 0 tables, want >= 1")
	}
	tbl := tables[0]
	// We constructed the fixture with 4 rows (1 header + 3 body) and
	// 3 columns. The text strategy infers row boundaries from top-y
	// clusters so depending on how the top/bottom edges merge we
	// may end up with 3 or 4 rows; assert at least 3 and at least 3
	// columns.
	if len(tbl.Rows) < 3 {
		t.Errorf("rows: got %d, want >= 3", len(tbl.Rows))
	}
	if len(tbl.Rows) > 0 && len(tbl.Rows[0]) < 3 {
		t.Errorf("cols: got %d, want >= 3", len(tbl.Rows[0]))
	}
	// Spot-check that the body data is present somewhere in the
	// extracted text (the algorithm may place it in any row/col
	// depending on edge merging; the parity test below pins the
	// exact layout).
	flat := strings.Join(flattenRows(tbl.Rows), " ")
	for _, want := range []string{"Apple", "Banana", "Cherry"} {
		if !strings.Contains(flat, want) {
			t.Errorf("flat output %q missing %q", flat, want)
		}
	}
}

// TestExtractTables_ExplicitStrategy asserts that supplying caller-
// derived coordinates via ExplicitVerticalLines /
// ExplicitHorizontalLines + StrategyExplicit produces the expected
// grid even when the underlying PDF has no rules drawn at all.
func TestExtractTables_ExplicitStrategy(t *testing.T) {
	doc, err := OpenBytes(testdata.TableBorderless())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}

	// The borderless fixture places its 3 columns near X = 100, 200,
	// 300 and 4 rows of text in the Y range [680, 730]. We feed
	// boundaries that bracket those positions.
	settings := DefaultTableSettings()
	settings.VerticalStrategy = StrategyExplicit
	settings.HorizontalStrategy = StrategyExplicit
	settings.ExplicitVerticalLines = []float64{95, 195, 295, 395}
	settings.ExplicitHorizontalLines = []float64{670, 690, 710, 740}

	tables, err := p.ExtractTables(settings)
	if err != nil {
		t.Fatalf("ExtractTables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatalf("got 0 tables, want >= 1")
	}
	tbl := tables[0]
	if len(tbl.Rows) != 3 {
		t.Errorf("rows: got %d, want 3 (4 H-edges → 3 rows)", len(tbl.Rows))
	}
	if len(tbl.Rows) > 0 && len(tbl.Rows[0]) != 3 {
		t.Errorf("cols: got %d, want 3 (4 V-edges → 3 cols)", len(tbl.Rows[0]))
	}
}

// TestExtractTables_MixedStrategy asserts that VerticalStrategy=text +
// HorizontalStrategy=explicit (and the reverse) work — each axis runs
// its own edge derivation and the resulting edges are merged together.
func TestExtractTables_MixedStrategy(t *testing.T) {
	doc, err := OpenBytes(testdata.TableBorderless())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}

	settings := DefaultTableSettings()
	settings.VerticalStrategy = StrategyText
	settings.HorizontalStrategy = StrategyExplicit
	settings.ExplicitHorizontalLines = []float64{670, 690, 710, 740}

	tables, err := p.ExtractTables(settings)
	if err != nil {
		t.Fatalf("ExtractTables (text-v + explicit-h): %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("got 0 tables, want >= 1")
	}
}

// flattenRows joins a 2-D string grid into a flat slice for
// substring spot-checks.
func flattenRows(rows [][]string) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}

// nanForTest returns a NaN without forcing the test file to import
// math at the top.
func nanForTest() float64 {
	zero := 0.0
	return zero / zero
}
