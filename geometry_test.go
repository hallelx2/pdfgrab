// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"math"
	"testing"
)

func TestBBoxNormalisation(t *testing.T) {
	// NewBBox should always normalise so the first corner is the
	// lower-left and the second corner is the upper-right.
	got := NewBBox(10, 20, 5, 5)
	want := BBox{X0: 5, Y0: 5, X1: 10, Y1: 20}
	if got != want {
		t.Errorf("NewBBox normalisation: got %+v, want %+v", got, want)
	}
}

func TestBBoxWidthHeight(t *testing.T) {
	b := BBox{X0: 10, Y0: 20, X1: 30, Y1: 50}
	if b.Width() != 20 {
		t.Errorf("Width = %v, want 20", b.Width())
	}
	if b.Height() != 30 {
		t.Errorf("Height = %v, want 30", b.Height())
	}
	if b.Area() != 600 {
		t.Errorf("Area = %v, want 600", b.Area())
	}
}

func TestBBoxIsZero(t *testing.T) {
	if !(BBox{}).IsZero() {
		t.Error("BBox{} should be zero")
	}
	if (BBox{X0: 1}).IsZero() {
		t.Error("non-zero BBox should not be zero")
	}
}

func TestBBoxUnion(t *testing.T) {
	a := BBox{X0: 0, Y0: 0, X1: 10, Y1: 10}
	b := BBox{X0: 5, Y0: 5, X1: 20, Y1: 30}
	got := a.Union(b)
	want := BBox{X0: 0, Y0: 0, X1: 20, Y1: 30}
	if got != want {
		t.Errorf("Union = %+v, want %+v", got, want)
	}

	// Disjoint bboxes: union spans both.
	c := BBox{X0: 100, Y0: 100, X1: 110, Y1: 110}
	got = a.Union(c)
	want = BBox{X0: 0, Y0: 0, X1: 110, Y1: 110}
	if got != want {
		t.Errorf("Union disjoint = %+v, want %+v", got, want)
	}
}

func TestBBoxIntersect(t *testing.T) {
	tests := []struct {
		name    string
		a, b    BBox
		want    BBox
		overlap bool
	}{
		{
			name:    "fully overlapping",
			a:       BBox{X0: 0, Y0: 0, X1: 10, Y1: 10},
			b:       BBox{X0: 5, Y0: 5, X1: 20, Y1: 20},
			want:    BBox{X0: 5, Y0: 5, X1: 10, Y1: 10},
			overlap: true,
		},
		{
			name:    "disjoint",
			a:       BBox{X0: 0, Y0: 0, X1: 10, Y1: 10},
			b:       BBox{X0: 20, Y0: 20, X1: 30, Y1: 30},
			overlap: false,
		},
		{
			name:    "share a single corner point (zero w + zero h)",
			a:       BBox{X0: 0, Y0: 0, X1: 10, Y1: 10},
			b:       BBox{X0: 10, Y0: 10, X1: 20, Y1: 20},
			overlap: false, // pdfplumber: w+h must be > 0; point touch has 0+0
		},
		{
			name:    "share horizontal edge (zero h, non-zero w)",
			a:       BBox{X0: 0, Y0: 0, X1: 10, Y1: 10},
			b:       BBox{X0: 0, Y0: 10, X1: 10, Y1: 20},
			want:    BBox{X0: 0, Y0: 10, X1: 10, Y1: 10},
			overlap: true, // pdfplumber: 10+0 > 0 → counted as overlap
		},
		{
			name:    "b fully inside a",
			a:       BBox{X0: 0, Y0: 0, X1: 100, Y1: 100},
			b:       BBox{X0: 10, Y0: 10, X1: 20, Y1: 20},
			want:    BBox{X0: 10, Y0: 10, X1: 20, Y1: 20},
			overlap: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.a.Intersect(tt.b)
			if ok != tt.overlap {
				t.Fatalf("overlap = %v, want %v", ok, tt.overlap)
			}
			if !ok {
				return
			}
			if got != tt.want {
				t.Errorf("Intersect = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBBoxContains(t *testing.T) {
	outer := BBox{X0: 0, Y0: 0, X1: 100, Y1: 100}
	inner := BBox{X0: 10, Y0: 10, X1: 20, Y1: 20}
	crossing := BBox{X0: 50, Y0: 50, X1: 150, Y1: 150}

	if !outer.Contains(inner) {
		t.Error("outer should contain inner")
	}
	if !outer.Contains(outer) {
		t.Error("bbox should contain itself")
	}
	if outer.Contains(crossing) {
		t.Error("outer should not contain crossing")
	}
}

func TestBBoxContainsPoint(t *testing.T) {
	b := BBox{X0: 0, Y0: 0, X1: 10, Y1: 10}
	if !b.ContainsPoint(5, 5) {
		t.Error("centre point should be contained")
	}
	if !b.ContainsPoint(0, 0) {
		t.Error("corner point should be contained (inclusive)")
	}
	if !b.ContainsPoint(10, 10) {
		t.Error("opposite corner should be contained (inclusive)")
	}
	if b.ContainsPoint(11, 5) {
		t.Error("outside point should not be contained")
	}
}

func TestBBoxSnap(t *testing.T) {
	b := BBox{X0: 99.7, Y0: 100.2, X1: 199.4, Y1: 200.8}
	got := b.Snap(1)
	want := BBox{X0: 100, Y0: 100, X1: 199, Y1: 201}
	if got != want {
		t.Errorf("Snap(1) = %+v, want %+v", got, want)
	}

	// Snap to 0.5 multiples.
	got = b.Snap(0.5)
	want = BBox{X0: 99.5, Y0: 100, X1: 199.5, Y1: 201}
	if got != want {
		t.Errorf("Snap(0.5) = %+v, want %+v", got, want)
	}

	// Snap with step 0 is a no-op.
	got = b.Snap(0)
	if got != b {
		t.Errorf("Snap(0) should be no-op, got %+v want %+v", got, b)
	}
}

func TestMergeBBoxes(t *testing.T) {
	// Empty: zero BBox.
	if got := MergeBBoxes(nil); !got.IsZero() {
		t.Errorf("MergeBBoxes(nil) = %+v, want zero", got)
	}

	// Single: same bbox.
	one := BBox{X0: 1, Y0: 2, X1: 3, Y1: 4}
	if got := MergeBBoxes([]BBox{one}); got != one {
		t.Errorf("MergeBBoxes([one]) = %+v, want %+v", got, one)
	}

	// Multiple.
	bs := []BBox{
		{X0: 10, Y0: 20, X1: 30, Y1: 40},
		{X0: 5, Y0: 25, X1: 25, Y1: 35},
		{X0: 15, Y0: 10, X1: 35, Y1: 30},
	}
	got := MergeBBoxes(bs)
	want := BBox{X0: 5, Y0: 10, X1: 35, Y1: 40}
	if got != want {
		t.Errorf("MergeBBoxes = %+v, want %+v", got, want)
	}
}

func TestBBoxOfChar(t *testing.T) {
	c := Char{Text: "x", X0: 1, Y0: 2, X1: 3, Y1: 4}
	got := BBoxOfChar(c)
	want := BBox{X0: 1, Y0: 2, X1: 3, Y1: 4}
	if got != want {
		t.Errorf("BBoxOfChar = %+v, want %+v", got, want)
	}
}

func TestBBoxOfChars(t *testing.T) {
	// Empty.
	if got := BBoxOfChars(nil); !got.IsZero() {
		t.Errorf("BBoxOfChars(nil) = %+v, want zero", got)
	}

	cs := []Char{
		{X0: 10, Y0: 20, X1: 15, Y1: 30},
		{X0: 15, Y0: 20, X1: 25, Y1: 30},
		{X0: 25, Y0: 20, X1: 35, Y1: 30},
	}
	got := BBoxOfChars(cs)
	want := BBox{X0: 10, Y0: 20, X1: 35, Y1: 30}
	if got != want {
		t.Errorf("BBoxOfChars = %+v, want %+v", got, want)
	}
}

func TestBBoxIntersectArea(t *testing.T) {
	// Sanity: small overlap area matches expected via math.
	a := BBox{X0: 0, Y0: 0, X1: 10, Y1: 10}
	b := BBox{X0: 8, Y0: 8, X1: 20, Y1: 20}
	overlap, ok := a.Intersect(b)
	if !ok {
		t.Fatal("expected overlap")
	}
	if math.Abs(overlap.Area()-4) > 1e-9 {
		t.Errorf("overlap area = %v, want 4", overlap.Area())
	}
}
