// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"math"
	"sort"
)

// This file is the Go port of pdfplumber/utils/clustering.py. The shape
// is the same — a 1-D agglomerative clusterer over a key extracted from
// each input — but the API uses generics so callers don't have to wrap
// every value in a dict-of-strings.
//
// The clustering primitives here are the load-bearing piece of the
// text-extraction pipeline: words are formed by clustering chars whose
// Y position is "close enough" (within YTolerance), and similarly for
// the layout grid that maps chars onto a fixed-width column.

// clusterFloat1D buckets sorted, deduped floats into clusters where
// each consecutive pair differs by <= tolerance. The output groups
// preserve the SORTED order of input — callers that need the original
// input order should use clusterObjects with preserveOrder=true.
//
// This is the workhorse pdfplumber calls cluster_list.
func clusterFloat1D(xs []float64, tolerance float64) [][]float64 {
	if len(xs) == 0 {
		return nil
	}
	sorted := make([]float64, len(xs))
	copy(sorted, xs)
	sort.Float64s(sorted)

	if tolerance == 0 {
		// Special-case to match pdfplumber: each value gets its own
		// singleton cluster. Without this branch we'd still cluster
		// equal values together, which differs from pdfplumber's
		// "tolerance==0 → no clustering" semantics.
		out := make([][]float64, len(sorted))
		for i, v := range sorted {
			out[i] = []float64{v}
		}
		return out
	}

	var groups [][]float64
	current := []float64{sorted[0]}
	last := sorted[0]
	for _, v := range sorted[1:] {
		if v <= last+tolerance {
			current = append(current, v)
		} else {
			groups = append(groups, current)
			current = []float64{v}
		}
		last = v
	}
	groups = append(groups, current)
	return groups
}

// makeClusterDict returns a map from each unique input value to the
// integer cluster id it lands in. Used internally by clusterObjects to
// translate per-object keys into group indices.
//
// We dedupe the input on the way in (pdfplumber does the same with
// `set(values)`) so that callers passing 10k chars don't pay 10k×
// sort-and-compare cost when many chars share the same key.
func makeClusterDict(values []float64, tolerance float64) map[float64]int {
	if len(values) == 0 {
		return map[float64]int{}
	}
	seen := make(map[float64]struct{}, len(values))
	uniq := make([]float64, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		uniq = append(uniq, v)
	}
	clusters := clusterFloat1D(uniq, tolerance)
	out := make(map[float64]int, len(uniq))
	for i, c := range clusters {
		for _, v := range c {
			out[v] = i
		}
	}
	return out
}

// clusterObjects groups xs by the float key returned from keyFn,
// bucketing keys that differ by <= tolerance into the same cluster.
//
// preserveOrder=false (the default in pdfplumber) emits clusters in
// ascending-key order; xs WITHIN a cluster keep their relative input
// order. preserveOrder=true keeps the original input order at both
// levels — which is what UseTextFlow text extraction wants, since the
// content stream's order conveys reading order in many PDFs. With
// preserveOrder=true, a NEW group is started every time consecutive
// items have different cluster ids (matching pdfplumber's
// itertools.groupby on un-sorted cluster_tuples).
//
// The function is generic over T so callers can cluster Chars, Words,
// or any other domain type without an interface{} cast on every
// element.
func clusterObjects[T any](xs []T, keyFn func(T) float64, tolerance float64, preserveOrder bool) [][]T {
	if len(xs) == 0 {
		return nil
	}

	keys := make([]float64, len(xs))
	for i, x := range xs {
		keys[i] = keyFn(x)
	}
	dict := makeClusterDict(keys, tolerance)

	type indexed struct {
		x   T
		cid int
	}
	buf := make([]indexed, len(xs))
	for i, x := range xs {
		buf[i] = indexed{x: x, cid: dict[keys[i]]}
	}

	if !preserveOrder {
		// Sort by cluster id; SliceStable so ties keep input order.
		sort.SliceStable(buf, func(i, j int) bool {
			return buf[i].cid < buf[j].cid
		})
	}

	// Emit a new group whenever the cluster id changes from the
	// previous entry. With preserveOrder=true this matches Python's
	// itertools.groupby on un-sorted cluster_tuples.
	var out [][]T
	current := []T{buf[0].x}
	currentID := buf[0].cid
	for _, e := range buf[1:] {
		if e.cid == currentID {
			current = append(current, e.x)
		} else {
			out = append(out, current)
			current = []T{e.x}
			currentID = e.cid
		}
	}
	out = append(out, current)
	return out
}

// groupObjectsByAttr buckets xs into groups that share the exact same
// value for the comparable key returned by keyFn. Order of input is
// preserved within each group; groups appear in the order their key
// first appears. This is the Go port of pdfplumber's itertools.groupby
// on (upright, *extra_attrs) — the outer grouping step in
// iter_extract_tuples.
//
// Unlike clusterObjects, this is an EXACT match on the key, not a
// tolerance-based clustering. The two are intentionally different
// operations and we name them differently to avoid confusion.
func groupObjectsByAttr[T any, K comparable](xs []T, keyFn func(T) K) [][]T {
	if len(xs) == 0 {
		return nil
	}
	var out [][]T
	current := []T{xs[0]}
	currentKey := keyFn(xs[0])
	for _, x := range xs[1:] {
		k := keyFn(x)
		if k == currentKey {
			current = append(current, x)
		} else {
			out = append(out, current)
			current = []T{x}
			currentKey = k
		}
	}
	out = append(out, current)
	return out
}

// dedupeChars removes near-duplicate chars (same text + position within
// tolerance). The classic case is a PDF that draws each glyph twice
// for an emboss/shadow effect — text extraction should report one
// glyph, not two. Mirrors pdfplumber.utils.text.dedupe_chars, with
// the same extra_attrs hook for letting callers tighten/loosen the
// "what counts as a duplicate" predicate.
//
// Supported extra_attrs: "fontname", "size". Other values are
// ignored (pdfplumber accepts any attribute name and indexes into the
// char dict; our Char struct doesn't have arbitrary keys, so we
// surface the two attrs callers actually use).
//
// The output is in the SAME ORDER as the input — the first occurrence
// of each cluster is kept and subsequent duplicates are dropped. This
// preserves content-stream order, which downstream code may rely on.
func dedupeChars(chars []Char, tolerance float64, extraAttrs []string) []Char {
	if len(chars) == 0 {
		return nil
	}

	// Group key: (upright, text, *extra_attrs). We collapse to a
	// string because (a) the key needs to be hashable for the equality
	// check, and (b) the extra_attrs slice is variable.
	keyOf := func(c Char) string {
		buf := make([]byte, 0, 32+len(c.Text)+len(c.FontName))
		if c.Upright {
			buf = append(buf, 'U')
		} else {
			buf = append(buf, 'u')
		}
		buf = append(buf, '\x00')
		buf = append(buf, c.Text...)
		for _, attr := range extraAttrs {
			buf = append(buf, '\x00')
			switch attr {
			case "fontname":
				buf = append(buf, c.FontName...)
			case "size":
				bits := math.Float64bits(c.FontSize)
				for i := 7; i >= 0; i-- {
					buf = append(buf, byte(bits>>(i*8)))
				}
			}
		}
		return string(buf)
	}

	type indexed struct {
		c   Char
		idx int
	}
	sorted := make([]indexed, len(chars))
	for i, c := range chars {
		sorted[i] = indexed{c: c, idx: i}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return keyOf(sorted[i].c) < keyOf(sorted[j].c)
	})

	keepIdx := make(map[int]struct{}, len(chars))

	// Walk equal-key runs; within each run cluster by Y0 then X0.
	for i := 0; i < len(sorted); {
		j := i + 1
		k := keyOf(sorted[i].c)
		for j < len(sorted) && keyOf(sorted[j].c) == k {
			j++
		}
		runChars := sorted[i:j]
		yClusters := clusterObjects(runChars, func(e indexed) float64 { return e.c.Y0 }, tolerance, false)
		for _, yc := range yClusters {
			xClusters := clusterObjects(yc, func(e indexed) float64 { return e.c.X0 }, tolerance, false)
			for _, xc := range xClusters {
				// Keep the char with the smallest original index in
				// the position bucket — preserves "first occurrence
				// wins" semantics relative to content-stream order.
				minIdx := xc[0].idx
				for _, e := range xc[1:] {
					if e.idx < minIdx {
						minIdx = e.idx
					}
				}
				keepIdx[minIdx] = struct{}{}
			}
		}
		i = j
	}

	out := make([]Char, 0, len(keepIdx))
	for i, c := range chars {
		if _, ok := keepIdx[i]; ok {
			out = append(out, c)
		}
	}
	return out
}

// float64Bits is a tiny re-export point that consolidates the math.
// Float64bits dependency so other tests/files don't have to import
// "math" just to compare floats. Left as a package-private helper for
// now — only dedupeChars's key construction uses it.
func float64Bits(f float64) uint64 { return math.Float64bits(f) }
