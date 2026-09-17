"""Generate golden-file expected outputs for pdfgrab's parity tests.

Run from the repo root after copying the fixture PDFs into
testdata/golden/:

    pip install pdfplumber
    python scripts/gen_golden.py

The script reads every *.pdf in testdata/golden/ and writes TWO
sibling JSON files:

  * <name>.expected.json         — words + extract_text (v0.1.0 tests).
  * <name>.tables.expected.json  — find_tables/extract output for the
                                    `lines` strategy (v0.2.0 tests).

Word goldens use image-space "top" / "bottom" translated into
PDF-user-space y0 / y1 so they match pdfgrab.Word fields directly.
Table goldens are the raw [[[str]]] output of pdfplumber's
Table.extract() — pdfgrab's parity test normalises whitespace before
comparing, so intra-cell line breaks ("A\\nB") match space-separated
output ("A B") and vice versa.

To regenerate after upgrading pdfplumber, simply re-run this script.
The file outputs are deterministic and stable.
"""

from __future__ import annotations

import json
import os
import sys

import pdfplumber

DIR = os.path.join("testdata", "golden")


def main() -> int:
    target = DIR if len(sys.argv) < 2 else sys.argv[1]
    pdfs = sorted(
        f for f in os.listdir(target) if f.endswith(".pdf")
    )
    if not pdfs:
        print(f"no .pdf files in {target}", file=sys.stderr)
        return 1
    for fname in pdfs:
        name = os.path.splitext(fname)[0]
        pdf_path = os.path.join(target, fname)
        out = {"name": name, "pages": []}
        with pdfplumber.open(pdf_path) as pdf:
            for p in pdf.pages:
                page = {
                    "number": p.page_number,
                    "width": p.width,
                    "height": p.height,
                    "extract_text": p.extract_text() or "",
                    "extract_words": [],
                }
                words = p.extract_words(
                    x_tolerance=3,
                    y_tolerance=3,
                    keep_blank_chars=False,
                    use_text_flow=False,
                    horizontal_ltr=True,
                    vertical_ttb=True,
                    extra_attrs=None,
                    split_at_punctuation=False,
                    expand_ligatures=True,
                )
                for w in words:
                    y1_user = p.height - w["top"]
                    y0_user = p.height - w["bottom"]
                    page["extract_words"].append(
                        {
                            "text": w["text"],
                            "x0": w["x0"],
                            "x1": w["x1"],
                            "y0": y0_user,
                            "y1": y1_user,
                            "upright": bool(w.get("upright", True)),
                            "direction": w.get("direction", "ltr"),
                        }
                    )
                out["pages"].append(page)
        expected = os.path.join(target, f"{name}.expected.json")
        with open(expected, "w", encoding="utf-8") as f:
            json.dump(out, f, ensure_ascii=False, indent=2)
        nwords = sum(len(pp["extract_words"]) for pp in out["pages"])
        print(f"wrote {expected}: {len(out['pages'])} pages, {nwords} words")

        # Also emit the tables-format golden so the v0.2.0 parity
        # test (TestGoldenTablesAgainstPdfplumber) has reference
        # output. Empty `pages[].tables` entries are fine — the test
        # just compares cell counts and contents, and a page with
        # zero detected tables produces an empty list on both sides.
        tables_out = {"name": name, "pages": []}
        with pdfplumber.open(pdf_path) as pdf:
            for p in pdf.pages:
                tbls = p.find_tables(
                    {"vertical_strategy": "lines", "horizontal_strategy": "lines"}
                )
                page_obj = {
                    "number": p.page_number,
                    "width": p.width,
                    "height": p.height,
                    "tables": [t.extract() for t in tbls],
                }
                tables_out["pages"].append(page_obj)
        tables_expected = os.path.join(target, f"{name}.tables.expected.json")
        with open(tables_expected, "w", encoding="utf-8") as f:
            json.dump(tables_out, f, ensure_ascii=False, indent=2)
        ntables = sum(len(pp["tables"]) for pp in tables_out["pages"])
        print(f"wrote {tables_expected}: {ntables} tables across all pages")
    return 0


if __name__ == "__main__":
    sys.exit(main())
