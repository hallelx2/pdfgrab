// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// CMap is a parsed ToUnicode CMap. For each glyph CID (a uint16 because
// PDF composite fonts use up to 2-byte CIDs), it returns the Unicode
// string that glyph represents — for almost all glyphs this is one
// rune, but ligature glyphs like `fi` and `ffi` legitimately produce
// multi-rune strings via the `bfchar` and `bfrange` CMap directives.
//
// We only parse the `bfchar` and `bfrange` directives plus the basic
// codespace-range bookkeeping. The other CMap features (`cidchar`,
// `cidrange`, `notdefrange`) describe how to MAP bytes to CIDs (the
// inverse direction), and for that we don't need a CMap at all — we
// treat the input bytes themselves as the CID stream for composite
// fonts. This is the same shortcut pdfminer.six takes for
// Identity-H/V-encoded CIDFonts, and it's correct for ~all real-world
// modern PDFs.
type CMap struct {
	// cid2unicode is the populated bfchar/bfrange table.
	cid2unicode map[uint16]string
}

// NewCMap returns an empty CMap. Use Parse to populate it from a
// ToUnicode stream.
func NewCMap() *CMap {
	return &CMap{cid2unicode: map[uint16]string{}}
}

// Lookup returns the Unicode string for cid and ok=true if present.
// Returns "", false otherwise — the caller decides whether to fall
// through to an Encoding-based mapping or to emit a `(cid:NNN)`
// placeholder.
func (c *CMap) Lookup(cid uint16) (string, bool) {
	if c == nil {
		return "", false
	}
	s, ok := c.cid2unicode[cid]
	return s, ok
}

// Size returns the number of entries in the CMap; useful for tests.
func (c *CMap) Size() int {
	if c == nil {
		return 0
	}
	return len(c.cid2unicode)
}

// ParseCMap reads a ToUnicode stream and populates a fresh CMap.
//
// The grammar is a tiny subset of PostScript — we tokenize the stream
// into (operator, operand) tuples and dispatch on operator keywords:
//
//   - `N beginbfchar ... endbfchar`: pairs of <src> <dst>, each repeated N times.
//   - `N beginbfrange ... endbfrange`: triples <srcLo> <srcHi> <dst>, repeated N
//     times. `dst` may be a hex string or an array of hex strings.
//
// All other CMap directives (`begincmap`, `begincodespacerange`,
// `beginnotdefrange`, `def`, `dict`, etc.) are recognised but their
// payloads are simply skipped — we don't need any of them to resolve
// CID → Unicode.
//
// Returns nil on a successful parse; returns an error only for I/O-
// level corruption (mismatched bf-block markers, unparseable hex).
// Truncated or malformed bf entries are silently dropped — the
// pdfminer reference does the same, and it's the right call: real
// PDFs have lots of weird CMaps and a strict parser breaks too easily.
func ParseCMap(data []byte) (*CMap, error) {
	c := NewCMap()
	toks := tokenizeCMap(data)
	i := 0
	for i < len(toks) {
		t := toks[i]
		switch t.kind {
		case tokKeyword:
			switch t.text {
			case "beginbfchar":
				end := findKeyword(toks, i+1, "endbfchar")
				if end < 0 {
					return nil, errors.New("cmap: beginbfchar without endbfchar")
				}
				parseBFChar(c, toks[i+1:end])
				i = end + 1
				continue
			case "beginbfrange":
				end := findKeyword(toks, i+1, "endbfrange")
				if end < 0 {
					return nil, errors.New("cmap: beginbfrange without endbfrange")
				}
				parseBFRange(c, toks[i+1:end])
				i = end + 1
				continue
			}
		}
		i++
	}
	return c, nil
}

// parseBFChar walks the body of a `beginbfchar ... endbfchar` block.
// Each entry is <srcHex> <dstHex>; we ignore the leading count integer
// because it's redundant with the actual block length and PDFs in the
// wild sometimes lie about it.
func parseBFChar(c *CMap, toks []cmapTok) {
	for i := 0; i+1 < len(toks); i += 2 {
		src, ok1 := toks[i].asHexBytes()
		dst, ok2 := toks[i+1].asHexBytes()
		if !ok1 || !ok2 {
			continue
		}
		cid := bytesToCID(src)
		c.cid2unicode[cid] = hexBytesToUTF16BE(dst)
	}
}

// parseBFRange walks `beginbfrange ... endbfrange`. Each entry is
// either <srcLo> <srcHi> <dstHex>, in which case dstHex is treated as
// the start of a contiguous Unicode range that increments by 1 per
// source CID; or <srcLo> <srcHi> [<dst1> <dst2> ...] where each
// successive CID maps to the next dst in the array. The second form
// is rare but legal (PDF 1.7 §9.10.3) — ligatures and other multi-
// glyph entries land here.
func parseBFRange(c *CMap, toks []cmapTok) {
	for i := 0; i+2 < len(toks); {
		lo, ok1 := toks[i].asHexBytes()
		hi, ok2 := toks[i+1].asHexBytes()
		if !ok1 || !ok2 {
			i += 3
			continue
		}
		loCID := bytesToCID(lo)
		hiCID := bytesToCID(hi)

		// Form 1: third token is a hex string → contiguous range.
		// Form 2: third token is '[' → array of dst strings.
		if toks[i+2].kind == tokHex || toks[i+2].kind == tokLiteralString {
			dst, _ := toks[i+2].asHexBytes()
			base := bytesToCID(dst)
			prefix := []byte{}
			if len(dst) > 2 {
				prefix = dst[:len(dst)-2]
			}
			for k := loCID; k <= hiCID; k++ {
				offset := uint16(uint32(base) + uint32(k-loCID))
				combined := append(append([]byte{}, prefix...), byte(offset>>8), byte(offset&0xff))
				c.cid2unicode[k] = hexBytesToUTF16BE(combined)
			}
			i += 3
			continue
		}
		if toks[i+2].kind == tokArrayBegin {
			// Walk until matching ']'.
			j := i + 3
			elems := make([][]byte, 0, int(hiCID-loCID+1))
			for j < len(toks) && toks[j].kind != tokArrayEnd {
				if b, ok := toks[j].asHexBytes(); ok {
					elems = append(elems, b)
				}
				j++
			}
			for k, b := range elems {
				cid := loCID + uint16(k)
				if cid > hiCID {
					break
				}
				c.cid2unicode[cid] = hexBytesToUTF16BE(b)
			}
			if j < len(toks) {
				i = j + 1
			} else {
				i = j
			}
			continue
		}
		i += 3
	}
}

// bytesToCID interprets the trailing 1 or 2 bytes of b as a big-endian
// CID. CMap source CIDs are written as hex strings — typically 2 bytes
// (`<0041>`), but single-byte simple-font CMaps use 1-byte sources
// (`<41>`). Treating them all as big-endian uint16 with zero padding
// on the left gives the right answer in both cases.
func bytesToCID(b []byte) uint16 {
	switch len(b) {
	case 0:
		return 0
	case 1:
		return uint16(b[0])
	default:
		return uint16(b[len(b)-2])<<8 | uint16(b[len(b)-1])
	}
}

// hexBytesToUTF16BE decodes a CMap destination payload from UTF-16BE
// to a Go string. The bytes are stored as big-endian UTF-16, possibly
// with surrogate pairs — that's the format PDF mandates for ToUnicode
// destination values (PDF 1.7 §9.10.3). We use the stdlib utf16
// decoder for surrogate handling.
func hexBytesToUTF16BE(b []byte) string {
	if len(b)%2 == 1 {
		b = append([]byte{0}, b...)
	}
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u16 = append(u16, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return string(utf16.Decode(u16))
}

// --- Tokenizer ---------------------------------------------------------------

type cmapTokKind int

const (
	tokInvalid cmapTokKind = iota
	tokNumber
	tokHex           // <DEADBEEF>
	tokLiteralString // (foo)
	tokName          // /Name
	tokKeyword       // bfchar, endbfchar, def, …
	tokArrayBegin    // [
	tokArrayEnd      // ]
	tokDictBegin     // <<
	tokDictEnd       // >>
)

type cmapTok struct {
	kind cmapTokKind
	text string // For tokKeyword, tokName, tokNumber.
	raw  []byte // For tokHex; the decoded bytes.
}

func (t cmapTok) asHexBytes() ([]byte, bool) {
	switch t.kind {
	case tokHex:
		return t.raw, true
	case tokLiteralString:
		return []byte(t.text), true
	default:
		return nil, false
	}
}

func findKeyword(toks []cmapTok, from int, kw string) int {
	for i := from; i < len(toks); i++ {
		if toks[i].kind == tokKeyword && toks[i].text == kw {
			return i
		}
	}
	return -1
}

// tokenizeCMap scans a CMap byte stream into typed tokens. The grammar
// is the same PostScript subset PDF inherits — whitespace separated,
// `%` line comments, `<...>` hex strings, `(...)` literal strings,
// `<<...>>` dicts, `[...]` arrays, `/foo` names, and bare keywords.
//
// We are NOT a fully general PS tokenizer — we just need enough to
// pluck the bf-blocks out. Anything we don't recognise becomes a
// keyword token; unrecognised keywords are skipped at the dispatch
// level above. This keeps the implementation small and forgiving.
func tokenizeCMap(data []byte) []cmapTok {
	var toks []cmapTok
	i := 0
	for i < len(data) {
		c := data[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '%':
			// Line comment to end-of-line.
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case c == '<':
			if i+1 < len(data) && data[i+1] == '<' {
				toks = append(toks, cmapTok{kind: tokDictBegin})
				i += 2
				break
			}
			// Hex string: <DEAD>.
			j := i + 1
			for j < len(data) && data[j] != '>' {
				j++
			}
			raw := hexDecode(data[i+1 : j])
			toks = append(toks, cmapTok{kind: tokHex, raw: raw})
			if j < len(data) {
				i = j + 1
			} else {
				i = j
			}
		case c == '>':
			if i+1 < len(data) && data[i+1] == '>' {
				toks = append(toks, cmapTok{kind: tokDictEnd})
				i += 2
				break
			}
			i++
		case c == '[':
			toks = append(toks, cmapTok{kind: tokArrayBegin})
			i++
		case c == ']':
			toks = append(toks, cmapTok{kind: tokArrayEnd})
			i++
		case c == '(':
			// Literal string: (foo\)bar). Backslash escapes.
			j := i + 1
			depth := 1
			var sb strings.Builder
			for j < len(data) && depth > 0 {
				switch data[j] {
				case '\\':
					if j+1 < len(data) {
						sb.WriteByte(data[j+1])
						j += 2
						continue
					}
					j++
				case '(':
					depth++
					sb.WriteByte(data[j])
					j++
				case ')':
					depth--
					if depth > 0 {
						sb.WriteByte(data[j])
					}
					j++
				default:
					sb.WriteByte(data[j])
					j++
				}
			}
			toks = append(toks, cmapTok{kind: tokLiteralString, text: sb.String()})
			i = j
		case c == '/':
			j := i + 1
			for j < len(data) && !isCMapDelim(data[j]) {
				j++
			}
			toks = append(toks, cmapTok{kind: tokName, text: string(data[i+1 : j])})
			i = j
		case (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.':
			j := i + 1
			for j < len(data) && !isCMapDelim(data[j]) {
				j++
			}
			text := string(data[i:j])
			if _, err := strconv.ParseFloat(text, 64); err == nil {
				toks = append(toks, cmapTok{kind: tokNumber, text: text})
			} else {
				toks = append(toks, cmapTok{kind: tokKeyword, text: text})
			}
			i = j
		default:
			// Bare keyword.
			j := i
			for j < len(data) && !isCMapDelim(data[j]) {
				j++
			}
			if j > i {
				toks = append(toks, cmapTok{kind: tokKeyword, text: string(data[i:j])})
			} else {
				j++
			}
			i = j
		}
	}
	return toks
}

func isCMapDelim(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' ||
		c == '<' || c == '>' || c == '[' || c == ']' ||
		c == '(' || c == ')' || c == '/' || c == '%'
}

// hexDecode parses the hex digits inside `<...>`, ignoring whitespace.
// Single-character last-nibbles are right-padded with `0` per the PDF
// spec ("<F>" → 0xF0).
func hexDecode(s []byte) []byte {
	var clean []byte
	for _, b := range s {
		if (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') {
			clean = append(clean, b)
		}
	}
	if len(clean)%2 == 1 {
		clean = append(clean, '0')
	}
	out := make([]byte, len(clean)/2)
	for i := 0; i < len(clean); i += 2 {
		hi, _ := hexDigit(clean[i])
		lo, _ := hexDigit(clean[i+1])
		out[i/2] = byte(hi<<4 | lo)
	}
	return out
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	default:
		return 0, false
	}
}

// Debug helper: format tokens as a string. Useful for tests when a
// CMap doesn't parse the way you expect.
func dumpTokens(toks []cmapTok) string {
	var sb strings.Builder
	for _, t := range toks {
		switch t.kind {
		case tokHex:
			sb.WriteString(fmt.Sprintf("<%x> ", t.raw))
		case tokLiteralString:
			sb.WriteString(fmt.Sprintf("(%s) ", t.text))
		case tokName:
			sb.WriteString(fmt.Sprintf("/%s ", t.text))
		case tokNumber:
			sb.WriteString(t.text)
			sb.WriteByte(' ')
		case tokKeyword:
			sb.WriteString(t.text)
			sb.WriteByte(' ')
		case tokArrayBegin:
			sb.WriteString("[ ")
		case tokArrayEnd:
			sb.WriteString("] ")
		case tokDictBegin:
			sb.WriteString("<< ")
		case tokDictEnd:
			sb.WriteString(">> ")
		}
	}
	return strings.TrimSpace(sb.String())
}
