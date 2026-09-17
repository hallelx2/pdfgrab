"""Generate font-coverage fixture PDFs for pdfgrab's tests.

    python scripts/gen_font_fixtures.py
    python scripts/gen_golden.py          # regenerate pdfplumber goldens

Writes minimal, uncompressed PDFs. They are built by hand rather than
with a PDF library on purpose: the point is to exercise the paths a
*producer* would normally hide from us.

The standard-14 pages carry **no /Widths and no /FontDescriptor**, which
is spec-legal (PDF 1.7 S9.6.2.2) and is exactly the case that hid two
font-metric bugs for four releases — a consumer has to supply the Adobe
AFM metrics itself or every glyph lands in the wrong place. A fixture
written by reportlab would embed the metrics and quietly test nothing.

Three font sizes per page, because one of those bugs was a missing
font-size factor on the descender: at a single size a wrong scale factor
is indistinguishable from a wrong constant. 8pt also catches word
over-merging, since an 8pt space is only 2.2pt wide.

Output splits by whether pdfplumber is a valid oracle:

  testdata/golden/  — the 12 Latin fonts. pdfplumber agrees, so
                      gen_golden.py generates the expected output and the
                      normal parity test picks them up automatically.

  testdata/fonts/   — Symbol, ZapfDingbats, /Differences. pdfplumber gets
                      these WRONG (it decodes Symbol with StandardEncoding
                      and returns "abgdep" where the answer is Greek), so a
                      generated golden would pin the wrong answer. These
                      are asserted directly in Go instead.
"""

from __future__ import annotations

import os
import sys

GOLDEN = os.path.join("testdata", "golden")
FONTS = os.path.join("testdata", "fonts")

# Glyphs whose AFM widths differ sharply (Helvetica i=222 vs m=833), so a
# flat-width fallback shows up immediately. The parenthesised negative is
# the shape that lost its sign in HAL-520.
LATIN_SAMPLE = "Wim illegible 3,142 (16,048)"

LATIN_12 = [
    ("Helvetica", LATIN_SAMPLE),
    ("Helvetica-Bold", LATIN_SAMPLE),
    ("Helvetica-Oblique", LATIN_SAMPLE),
    ("Helvetica-BoldOblique", LATIN_SAMPLE),
    ("Times-Roman", LATIN_SAMPLE),
    ("Times-Bold", LATIN_SAMPLE),
    ("Times-Italic", LATIN_SAMPLE),
    ("Times-BoldItalic", LATIN_SAMPLE),
    ("Courier", LATIN_SAMPLE),
    ("Courier-Bold", LATIN_SAMPLE),
    ("Courier-Oblique", LATIN_SAMPLE),
    ("Courier-BoldOblique", LATIN_SAMPLE),
]

# These byte codes are alpha beta gamma delta epsilon pi in Symbol's own
# encoding, NOT Latin letters. A consumer applying StandardEncoding
# decodes "abgdep" — the bug HAL-481 fixed.
SYMBOL_FONTS = [
    ("Symbol", "abgdep"),
    ("ZapfDingbats", "\\061\\062\\063"),
]

SIZES = (8, 12, 24)


def esc(s: str) -> str:
    """Escape parens for a PDF literal, leaving \\NNN octal intact."""
    return s.replace("(", r"\(").replace(")", r"\)")


def serialise(objs: list[bytes]) -> bytes:
    out = bytearray(b"%PDF-1.7\n")
    offsets = [0] * (len(objs) + 1)
    for i, body in enumerate(objs, start=1):
        offsets[i] = len(out)
        out += f"{i} 0 obj\n".encode("latin-1") + body + b"\nendobj\n"
    xref_at = len(out)
    out += f"xref\n0 {len(objs) + 1}\n".encode("latin-1")
    out += b"0000000000 65535 f \n"
    for i in range(1, len(objs) + 1):
        out += f"{offsets[i]:010d} 00000 n \n".encode("latin-1")
    out += (
        f"trailer\n<< /Size {len(objs) + 1} /Root 1 0 R >>\n"
        f"startxref\n{xref_at}\n%%EOF\n"
    ).encode("latin-1")
    return bytes(out)


def build_pdf(pages: list[tuple[str, str]]) -> bytes:
    """One page per (basefont, text) entry, three font sizes each."""
    objs: list[bytes] = [b"", b""]  # 1=Catalog, 2=Pages, filled in below

    def add(s: str) -> int:
        objs.append(s.encode("latin-1"))
        return len(objs)

    kids: list[int] = []
    for basefont, text in pages:
        font_num = add(f"<< /Type /Font /Subtype /Type1 /BaseFont /{basefont} >>")
        lines, y = [], 700
        for size in SIZES:
            lines.append(f"BT /F1 {size} Tf 72 {y} Td ({esc(text)}) Tj ET")
            y -= 60
        stream = "\n".join(lines)
        content_num = add(f"<< /Length {len(stream)} >>\nstream\n{stream}\nendstream")
        kids.append(
            add(
                "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                f"/Resources << /Font << /F1 {font_num} 0 R >> "
                f"/ProcSet [/PDF /Text] >> /Contents {content_num} 0 R >>"
            )
        )

    kid_refs = " ".join(f"{k} 0 R" for k in kids)
    objs[0] = b"<< /Type /Catalog /Pages 2 0 R >>"
    objs[1] = f"<< /Type /Pages /Kids [{kid_refs}] /Count {len(kids)} >>".encode(
        "latin-1"
    )
    return serialise(objs)


def build_differences_pdf() -> bytes:
    """A Helvetica font whose /Differences array renames glyphs.

    Covers the resolver split from HAL-481: Symbol's names are real AGL
    entries and must resolve in any font, while ZapfDingbats' "aNN" names
    are font-specific and must NOT — a Latin font naming "a1" means its
    own glyph, not U+2701 SCISSORS.
    """
    stream = (
        "BT /F1 18 Tf 72 700 Td (\\101\\102\\103\\104) Tj ET\n"
        "BT /F1 18 Tf 72 640 Td (\\105\\106) Tj ET"
    )
    objs = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        (
            "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
            "/Resources << /Font << /F1 4 0 R >> /ProcSet [/PDF /Text] >> "
            "/Contents 6 0 R >>"
        ).encode("latin-1"),
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding 5 0 R >>",
        # A=Alpha, B=universal, C=club (real AGL names, must resolve),
        # D=a1 (dingbat name, must NOT resolve globally),
        # E=summation, F=partialdiff.
        (
            "<< /Type /Encoding /Differences "
            "[65 /Alpha /universal /club /a1 /summation /partialdiff] >>"
        ).encode("latin-1"),
        f"<< /Length {len(stream)} >>\nstream\n{stream}\nendstream".encode("latin-1"),
    ]
    return serialise(objs)


def main() -> int:
    os.makedirs(GOLDEN, exist_ok=True)
    os.makedirs(FONTS, exist_ok=True)
    written = []

    def emit(path: str, data: bytes, note: str) -> None:
        with open(path, "wb") as f:
            f.write(data)
        written.append((path, note))

    emit(
        os.path.join(GOLDEN, "fonts-standard14.pdf"),
        build_pdf(LATIN_12),
        f"{len(LATIN_12)} pages, pdfplumber golden",
    )
    emit(
        os.path.join(FONTS, "symbol.pdf"),
        build_pdf(SYMBOL_FONTS),
        "2 pages, asserted in Go (pdfplumber is wrong here)",
    )
    emit(
        os.path.join(FONTS, "differences.pdf"),
        build_differences_pdf(),
        "1 page, asserted in Go",
    )

    for path, note in written:
        print(f"wrote {path} ({note}, {os.path.getsize(path)} bytes)")
    print("\nnow run: python scripts/gen_golden.py")
    return 0


if __name__ == "__main__":
    sys.exit(main())
