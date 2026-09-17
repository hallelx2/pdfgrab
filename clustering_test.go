// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"reflect"
	"testing"
)

func TestClusterFloat1D(t *testing.T) {
	tests := []struct {
		name      string
		in        []float64
		tolerance float64
		want      [][]float64
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name:      "single",
			in:        []float64{42},
			tolerance: 5,
			want:      [][]float64{{42}},
		},
		{
			name:      "all within tolerance",
			in:        []float64{1, 2, 3, 4, 5},
			tolerance: 1.5,
			want:      [][]float64{{1, 2, 3, 4, 5}},
		},
		{
			name:      "out of order input gets sorted",
			in:        []float64{5, 1, 3, 4, 2},
			tolerance: 1.5,
			want:      [][]float64{{1, 2, 3, 4, 5}},
		},
		{
			name:      "two clusters",
			in:        []float64{1, 2, 10, 11, 12},
			tolerance: 2,
			want:      [][]float64{{1, 2}, {10, 11, 12}},
		},
		{
			name:      "tolerance 0 → singletons",
			in:        []float64{1, 1, 2, 2, 3},
			tolerance: 0,
			want:      [][]float64{{1}, {1}, {2}, {2}, {3}},
		},
		{
			name:      "chain growth (each in range of last)",
			in:        []float64{1, 1.4, 1.7, 2.0, 2.3, 2.6, 100},
			tolerance: 0.5,
			want:      [][]float64{{1, 1.4, 1.7, 2.0, 2.3, 2.6}, {100}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clusterFloat1D(tt.in, tt.tolerance)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMakeClusterDict(t *testing.T) {
	// Duplicates should be deduped before clustering, but the result
	// map should contain an entry for every unique input value.
	got := makeClusterDict([]float64{1, 1, 2, 10, 10, 11}, 2)
	// Cluster 0 = {1, 2}, cluster 1 = {10, 11}.
	if got[1] != got[2] {
		t.Errorf("1 and 2 should be in same cluster, got %d vs %d", got[1], got[2])
	}
	if got[10] != got[11] {
		t.Errorf("10 and 11 should be in same cluster, got %d vs %d", got[10], got[11])
	}
	if got[1] == got[10] {
		t.Errorf("1 and 10 should be in different clusters, both got %d", got[1])
	}
}

// TestClusterObjects exercises both preserveOrder modes on a tiny set
// of struct-valued inputs.
func TestClusterObjects(t *testing.T) {
	type pt struct {
		x   float64
		tag string
	}
	xs := []pt{
		{x: 1, tag: "a"},
		{x: 10, tag: "b"},
		{x: 2, tag: "c"},
		{x: 11, tag: "d"},
	}

	// preserveOrder=false: clusters sorted by key, items sorted within
	// by input order (a,c then b,d).
	got := clusterObjects(xs, func(p pt) float64 { return p.x }, 5, false)
	want := [][]pt{
		{{1, "a"}, {2, "c"}},
		{{10, "b"}, {11, "d"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preserveOrder=false: got %v, want %v", got, want)
	}

	// preserveOrder=true: groups in input order; new group whenever
	// cluster id changes from previous entry.
	// Input cluster ids: a=0, b=1, c=0, d=1 → groups: [a], [b], [c], [d].
	got = clusterObjects(xs, func(p pt) float64 { return p.x }, 5, true)
	want = [][]pt{
		{{1, "a"}},
		{{10, "b"}},
		{{2, "c"}},
		{{11, "d"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preserveOrder=true: got %v, want %v", got, want)
	}
}

func TestGroupObjectsByAttr(t *testing.T) {
	type item struct {
		key string
		val int
	}
	xs := []item{
		{"a", 1},
		{"a", 2},
		{"b", 3},
		{"a", 4}, // restart "a" group — exact-match groupby preserves order
	}
	got := groupObjectsByAttr(xs, func(i item) string { return i.key })
	want := [][]item{
		{{"a", 1}, {"a", 2}},
		{{"b", 3}},
		{{"a", 4}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedupeCharsSimple(t *testing.T) {
	// Two pairs of overlapping glyphs (shadow effect) drawn at near-
	// identical positions. After dedupe we expect one of each pair.
	chars := []Char{
		{Text: "A", X0: 0, Y0: 0, X1: 10, Y1: 10, FontName: "F", FontSize: 12, Upright: true},
		{Text: "A", X0: 0.2, Y0: 0.3, X1: 10.2, Y1: 10.3, FontName: "F", FontSize: 12, Upright: true},
		{Text: "B", X0: 11, Y0: 0, X1: 20, Y1: 10, FontName: "F", FontSize: 12, Upright: true},
		{Text: "B", X0: 11.1, Y0: 0.2, X1: 20.1, Y1: 10.2, FontName: "F", FontSize: 12, Upright: true},
	}
	got := dedupeChars(chars, 1, []string{"fontname", "size"})
	if len(got) != 2 {
		t.Fatalf("got %d chars after dedupe, want 2", len(got))
	}
	// First occurrence should be kept.
	if got[0].Text != "A" || got[0].X0 != 0 {
		t.Errorf("first kept char = %+v, want first 'A'", got[0])
	}
	if got[1].Text != "B" || got[1].X0 != 11 {
		t.Errorf("second kept char = %+v, want first 'B'", got[1])
	}
}

func TestDedupeCharsKeepsDifferentText(t *testing.T) {
	// Two glyphs at identical positions but different text should NOT
	// be deduped.
	chars := []Char{
		{Text: "A", X0: 0, Y0: 0, X1: 10, Y1: 10, FontName: "F", FontSize: 12, Upright: true},
		{Text: "B", X0: 0, Y0: 0, X1: 10, Y1: 10, FontName: "F", FontSize: 12, Upright: true},
	}
	got := dedupeChars(chars, 1, []string{"fontname", "size"})
	if len(got) != 2 {
		t.Fatalf("got %d chars, want 2 (different text → keep both)", len(got))
	}
}

func TestDedupeCharsKeepsDifferentFont(t *testing.T) {
	// Same text at same position but different fontname → keep both
	// when fontname is in extra_attrs.
	chars := []Char{
		{Text: "A", X0: 0, Y0: 0, X1: 10, Y1: 10, FontName: "Helvetica", FontSize: 12, Upright: true},
		{Text: "A", X0: 0, Y0: 0, X1: 10, Y1: 10, FontName: "Times", FontSize: 12, Upright: true},
	}
	got := dedupeChars(chars, 1, []string{"fontname"})
	if len(got) != 2 {
		t.Fatalf("got %d chars, want 2 (different fontname → keep both)", len(got))
	}

	// Without fontname in extra_attrs → drop the duplicate.
	got = dedupeChars(chars, 1, nil)
	if len(got) != 1 {
		t.Fatalf("got %d chars, want 1 (no extra_attrs → dedupe by text+pos)", len(got))
	}
}

func TestDedupeCharsEmpty(t *testing.T) {
	if got := dedupeChars(nil, 1, nil); got != nil {
		t.Errorf("dedupeChars(nil) = %v, want nil", got)
	}
}
