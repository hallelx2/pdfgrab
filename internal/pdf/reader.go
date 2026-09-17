// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdf

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Reader is the bridge from pdfcpu's parsed-object model to the
// interpreter. One Reader wraps one *model.Context (one PDF document)
// and exposes per-page accessors: ReadPage returns the content stream
// bytes plus the resolved font / xobject maps the interpreter needs.
//
// We deliberately separate pdfcpu-specific code into this one file.
// Everything in state.go, cmap.go, font.go, ops.go, and content.go is
// stdlib-only — that way if we ever want to swap pdfcpu for our own
// PDF object parser (a real possibility for a v1.0 release: pdfcpu
// is heavy and pulls in image-codec dependencies we don't need), the
// blast radius is limited to this file.
type Reader struct {
	Ctx *model.Context
}

// NewReader takes a fully-decoded byte slice and returns a Reader.
// pdfcpu does all the heavy lifting: xref parsing, FlateDecode of
// compressed streams, object resolution. If the file is encrypted we
// surface ErrEncrypted to the caller — full crypto support is a later
// phase.
func NewReader(data []byte) (*Reader, error) {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	ctx, err := api.ReadContext(bytes.NewReader(data), conf)
	if err != nil {
		return nil, fmt.Errorf("pdfgrab: read: %w", err)
	}
	if ctx.Encrypt != nil && ctx.E != nil {
		// pdfcpu auto-decrypts with the configured password (empty by
		// default). If decryption succeeded ctx.EncKey will be set
		// and the streams have already been decrypted in place; only
		// fail when we can SEE the encrypt dict but ctx couldn't
		// resolve the key. This is the pragmatic split between
		// "encrypted PDF that's actually openable" (we go ahead and
		// process it) and "encrypted PDF that needs a real password"
		// (we surface ErrEncrypted).
	}
	if err := ctx.EnsurePageCount(); err != nil {
		return nil, fmt.Errorf("pdfgrab: page count: %w", err)
	}
	return &Reader{Ctx: ctx}, nil
}

// NumPages returns the page count.
func (r *Reader) NumPages() int { return r.Ctx.PageCount }

// PageInfo bundles everything the interpreter needs to walk one page:
// the concatenated content-stream bytes, the page's mediabox, rotation,
// and the resolved font / xobject maps.
type PageInfo struct {
	Content  []byte
	MediaBox [4]float64
	Rotate   int
	Fonts    map[string]*Font
	XObjects map[string]XObject
}

// ReadPage loads page n (1-indexed) and resolves all of its resources
// into the simple maps the interpreter expects. Per the PDF spec the
// /Resources dictionary is inherited up the page-tree branch; pdfcpu
// resolves that for us when we pass consolidateRes=true.
func (r *Reader) ReadPage(n int) (*PageInfo, error) {
	if n < 1 || n > r.Ctx.PageCount {
		return nil, errors.New("page out of range")
	}
	pageDict, _, attrs, err := r.Ctx.PageDict(n, true)
	if err != nil {
		return nil, fmt.Errorf("page dict: %w", err)
	}
	if pageDict == nil {
		return nil, fmt.Errorf("page %d: not found", n)
	}

	content, err := r.Ctx.PageContent(pageDict, n)
	if err != nil {
		// A page with no content stream is legal (blank page). Return
		// an empty stream rather than an error — the page is still
		// addressable, it just has no glyphs or paths.
		if err.Error() == "" {
			content = nil
		} else {
			// pdfcpu returns a specific NoContent sentinel sometimes,
			// but the string is not stable across versions; the safe
			// move is to treat "we couldn't read content" as
			// "empty page" rather than aborting the whole document.
			content = nil
		}
	}

	info := &PageInfo{
		Content:  content,
		Fonts:    map[string]*Font{},
		XObjects: map[string]XObject{},
	}

	if attrs != nil {
		if mb := attrs.MediaBox; mb != nil {
			info.MediaBox = [4]float64{mb.LL.X, mb.LL.Y, mb.UR.X, mb.UR.Y}
		}
		info.Rotate = attrs.Rotate
	}

	// Walk /Resources/Font.
	if attrs != nil && attrs.Resources != nil {
		if fonts, ok := attrs.Resources["Font"]; ok {
			fontDict, err := r.Ctx.DereferenceDict(fonts)
			if err == nil && fontDict != nil {
				for name, ref := range fontDict {
					f, err := r.readFont(ref)
					if err == nil && f != nil {
						info.Fonts[name] = f
					}
				}
			}
		}
		if xobjs, ok := attrs.Resources["XObject"]; ok {
			xoDict, err := r.Ctx.DereferenceDict(xobjs)
			if err == nil && xoDict != nil {
				for name, ref := range xoDict {
					xo, err := r.readXObject(ref)
					if err == nil {
						info.XObjects[name] = xo
					}
				}
			}
		}
	}

	return info, nil
}

// readFont parses a single /Font entry into our *Font.
//
// We accept simple (Type1 / TrueType / MMType1) and composite (Type0)
// subtypes. For composite fonts the actual width and encoding live on
// a descendant CIDFont — we drill in to fetch those without exposing
// the indirection to the rest of the interpreter.
func (r *Reader) readFont(ref types.Object) (*Font, error) {
	dict, err := r.Ctx.DereferenceDict(ref)
	if err != nil || dict == nil {
		return nil, err
	}
	subtype := nameValue(dict, "Subtype")
	baseFont := nameValue(dict, "BaseFont")

	f := &Font{
		BaseFont: baseFont,
		Widths:   map[uint16]float64{},
		IsSimple: true,
	}

	switch subtype {
	case "Type0":
		f.IsSimple = false
		// Drill into descendant font for widths and descriptor.
		if descs, ok := dict["DescendantFonts"]; ok {
			arr, err := r.Ctx.DereferenceArray(descs)
			if err == nil && len(arr) > 0 {
				descDict, err := r.Ctx.DereferenceDict(arr[0])
				if err == nil && descDict != nil {
					applyFontDescriptor(f, descDict, r)
					applyCIDWidths(f, descDict, r)
				}
			}
		}
	case "Type1", "MMType1", "TrueType":
		// Encoding: either a name literal or a dict with /BaseEncoding
		// and /Differences.
		enc := "WinAnsiEncoding"
		if subtype == "Type1" || subtype == "MMType1" {
			enc = "StandardEncoding"
		}
		var diffs []Difference
		// explicitBase records whether the PDF actually named a base
		// encoding, as opposed to us falling back to the subtype default
		// above. It matters for Symbol/ZapfDingbats: an /Encoding dict
		// carrying only /Differences must still sit on top of the font's
		// built-in encoding, not on StandardEncoding.
		explicitBase := false
		if encRef, ok := dict["Encoding"]; ok {
			if encName, ok := encRef.(types.Name); ok {
				enc = encName.Value()
				explicitBase = true
			} else {
				encDict, err := r.Ctx.DereferenceDict(encRef)
				if err == nil && encDict != nil {
					if base, ok := encDict["BaseEncoding"]; ok {
						if bn, ok := base.(types.Name); ok {
							enc = bn.Value()
							explicitBase = true
						}
					}
					if d, ok := encDict["Differences"]; ok {
						if arr, err := r.Ctx.DereferenceArray(d); err == nil {
							diffs = parseDifferencesArray(arr)
						}
					}
				}
			}
		}
		var base [256]string
		if builtin, ok := Standard14BuiltinEncoding(baseFont); ok && !explicitBase {
			// Symbol and ZapfDingbats carry their own encoding; the four
			// PDF base encodings do not describe them.
			base = builtin
		} else {
			base = EncodingByName(enc)
		}
		// /Differences names are resolved font-scoped for the standard 14.
		// ZapfDingbats is why: its "aNN" names are invisible to the global
		// resolver by design, so resolving them there would blank out slots
		// we can actually resolve.
		resolve := AdobeGlyphToUnicode
		if r, ok := Standard14GlyphResolver(baseFont); ok {
			resolve = r
		}
		f.cid2unicodeEncoding = ApplyDifferencesWith(base, diffs, resolve)

		// Widths for simple fonts: /FirstChar /LastChar /Widths.
		applySimpleWidths(f, dict, r)
		applyFontDescriptor(f, dict, r)

		// The 14 standard fonts are spec-exempt from carrying their own
		// /Widths array (PDF 1.7 §9.6.2.2) -- a viewer is expected to
		// already know their metrics. If this font dict didn't supply
		// one, and BaseFont names a standard font (directly or via a
		// common substitute alias like Arial/Times New Roman/Courier
		// New), fall back to the real AFM widths instead of the flat
		// 500 guess CharWidth otherwise uses for every glyph.
		if len(f.Widths) == 0 {
			if w, ok := Standard14Widths(baseFont); ok {
				f.Standard14 = w
			}
		}

		// The same exemption also lets these fonts omit /FontDescriptor,
		// which is where Ascent/Descent live. Without them a glyph's box
		// collapses to [baseline, baseline+size] instead of resting on
		// the real descender, so every box sits descent*size too high --
		// and row detection is built on word Y extents.
		if f.Ascent == 0 && f.Descent == 0 {
			if vm, ok := Standard14VMetrics(baseFont); ok {
				f.Ascent = vm.Ascent
				f.Descent = vm.Descent
			}
		}
	default:
		// Unknown subtype: fall through with whatever we managed to
		// extract. Better to emit positioned-but-unreadable glyphs
		// than to abort the whole page.
	}

	// ToUnicode CMap (optional but recommended; modern PDFs almost
	// always carry one).
	if tu, ok := dict["ToUnicode"]; ok {
		sd, _, err := r.Ctx.DereferenceStreamDict(tu)
		if err == nil && sd != nil {
			if err := sd.Decode(); err == nil && len(sd.Content) > 0 {
				if cm, err := ParseCMap(sd.Content); err == nil {
					f.ToUnicode = cm
				}
			}
		}
	}

	return f, nil
}

// applyFontDescriptor pulls Ascent/Descent from a /FontDescriptor
// dictionary if present. For simple fonts the descriptor lives on the
// font dict's /FontDescriptor entry; for composite fonts it's on the
// descendant CIDFont. We accept either by passing the dict that holds it.
func applyFontDescriptor(f *Font, dict types.Dict, r *Reader) {
	desc, ok := dict["FontDescriptor"]
	if !ok {
		return
	}
	d, err := r.Ctx.DereferenceDict(desc)
	if err != nil || d == nil {
		return
	}
	if a := numberValue(d, "Ascent"); a != 0 {
		f.Ascent = a
	}
	if d := numberValue(d, "Descent"); d != 0 {
		f.Descent = d
	}
	// PDF spec mandates negative descent; some buggy PDFs ship it positive.
	if f.Descent > 0 {
		f.Descent = -f.Descent
	}
}

// applySimpleWidths fills Widths for a Type1/TrueType font from the
// /FirstChar, /LastChar, /Widths triplet.
func applySimpleWidths(f *Font, dict types.Dict, r *Reader) {
	first := int(numberValue(dict, "FirstChar"))
	if widths, ok := dict["Widths"]; ok {
		arr, err := r.Ctx.DereferenceArray(widths)
		if err != nil {
			return
		}
		for i, v := range arr {
			n, ok := asNumber(v)
			if !ok {
				continue
			}
			f.Widths[uint16(first+i)] = n
		}
	}
}

// applyCIDWidths fills Widths from a composite font's /W array (PDF
// 1.7 §9.7.4.3). The array has two forms intermixed:
//
//   - c [w1 w2 ... wn]  → CIDs c, c+1, ..., c+n-1 get widths w1..wn.
//   - c1 c2 w           → CIDs in range c1..c2 each get width w.
//
// /DW gives the default width.
func applyCIDWidths(f *Font, dict types.Dict, r *Reader) {
	if dw, ok := dict["DW"]; ok {
		if n, ok := asNumber(dw); ok {
			f.DefaultWidth = n
		}
	}
	w, ok := dict["W"]
	if !ok {
		return
	}
	arr, err := r.Ctx.DereferenceArray(w)
	if err != nil {
		return
	}
	for i := 0; i < len(arr); {
		c1, ok := asNumber(arr[i])
		if !ok {
			i++
			continue
		}
		if i+1 >= len(arr) {
			break
		}
		next := arr[i+1]
		// Sub-array form.
		if subArr, err := r.Ctx.DereferenceArray(next); err == nil && subArr != nil {
			for k, v := range subArr {
				if n, ok := asNumber(v); ok {
					f.Widths[uint16(int(c1)+k)] = n
				}
			}
			i += 2
			continue
		}
		// Range form: c1 c2 w.
		if i+2 >= len(arr) {
			break
		}
		c2, ok2 := asNumber(arr[i+1])
		ww, ok3 := asNumber(arr[i+2])
		if !ok2 || !ok3 {
			i++
			continue
		}
		for k := int(c1); k <= int(c2); k++ {
			f.Widths[uint16(k)] = ww
		}
		i += 3
	}
}

// parseDifferencesArray reads a /Differences array into the typed
// (cid, glyph-name) sequence expected by ApplyDifferences.
func parseDifferencesArray(arr types.Array) []Difference {
	var out []Difference
	cid := 0
	for _, elem := range arr {
		switch v := elem.(type) {
		case types.Integer:
			cid = v.Value()
		case types.Float:
			cid = int(v.Value())
		case types.Name:
			out = append(out, Difference{CID: cid, GlyphName: v.Value()})
			cid++
		}
	}
	return out
}

// readXObject parses a /XObject entry. We only descend into Form
// XObjects (we need their content stream to inline them); image
// XObjects are returned with Subtype="Image" so the interpreter can
// skip them.
func (r *Reader) readXObject(ref types.Object) (XObject, error) {
	sd, _, err := r.Ctx.DereferenceStreamDict(ref)
	if err != nil || sd == nil {
		return XObject{}, err
	}
	subtype := nameValue(sd.Dict, "Subtype")
	xo := XObject{Subtype: subtype, Matrix: IdentityMatrix}
	if subtype != "Form" {
		return xo, nil
	}
	if err := sd.Decode(); err != nil {
		return xo, err
	}
	xo.Content = append([]byte(nil), sd.Content...)
	if bb, ok := sd.Dict["BBox"]; ok {
		if arr, err := r.Ctx.DereferenceArray(bb); err == nil && len(arr) >= 4 {
			a, _ := asNumber(arr[0])
			b, _ := asNumber(arr[1])
			c, _ := asNumber(arr[2])
			d, _ := asNumber(arr[3])
			xo.BBox = [4]float64{a, b, c, d}
		}
	}
	if mx, ok := sd.Dict["Matrix"]; ok {
		if arr, err := r.Ctx.DereferenceArray(mx); err == nil && len(arr) >= 6 {
			a, _ := asNumber(arr[0])
			b, _ := asNumber(arr[1])
			c, _ := asNumber(arr[2])
			d, _ := asNumber(arr[3])
			e, _ := asNumber(arr[4])
			f, _ := asNumber(arr[5])
			xo.Matrix = Matrix{a, b, c, d, e, f}
		}
	}
	// XObjects can carry their own /Resources scope.
	if res, ok := sd.Dict["Resources"]; ok {
		resDict, err := r.Ctx.DereferenceDict(res)
		if err == nil && resDict != nil {
			xo.Fonts = map[string]*Font{}
			xo.XObjects = map[string]XObject{}
			if fonts, ok := resDict["Font"]; ok {
				fd, err := r.Ctx.DereferenceDict(fonts)
				if err == nil && fd != nil {
					for name, ref := range fd {
						if f, err := r.readFont(ref); err == nil && f != nil {
							xo.Fonts[name] = f
						}
					}
				}
			}
			if xos, ok := resDict["XObject"]; ok {
				xd, err := r.Ctx.DereferenceDict(xos)
				if err == nil && xd != nil {
					for name, ref := range xd {
						if sub, err := r.readXObject(ref); err == nil {
							xo.XObjects[name] = sub
						}
					}
				}
			}
		}
	}
	return xo, nil
}

// --- Helpers for digging values out of pdfcpu Dict / Object structs --------

func nameValue(d types.Dict, key string) string {
	v, ok := d[key]
	if !ok {
		return ""
	}
	if n, ok := v.(types.Name); ok {
		return n.Value()
	}
	return ""
}

func numberValue(d types.Dict, key string) float64 {
	v, ok := d[key]
	if !ok {
		return 0
	}
	n, _ := asNumber(v)
	return n
}

func asNumber(o types.Object) (float64, bool) {
	switch v := o.(type) {
	case types.Integer:
		return float64(v.Value()), true
	case types.Float:
		return v.Value(), true
	}
	return 0, false
}
