"""Generate text-strategy golden files for pdfgrab's parity tests.

Run from the repo root after copying any new borderless / text-strategy
fixture PDFs into testdata/golden/:

    pip install pdfplumber
    python scripts/capture_pdfplumber_text_golden.py

The script reads every *.pdf in testdata/golden/ that has a sibling
.tables-text.target file (the marker says "this fixture is in the
text-strategy parity set") and writes
<name>.tables-text.expected.json — pdfplumber's find_tables output
under {vertical_strategy: 'text', horizontal_strategy: 'text'} with
matching MinWordsVertical / MinWordsHorizontal defaults (3 / 1).

We separate the text-strategy goldens from the line-strategy ones
(*.tables.expected.json) because the same fixture may produce
different tables depending on the strategy, and we want to assert
parity per strategy.

Re-run to refresh after upgrading pdfplumber or changing the
target list.
"""

from __future__ import annotations

import json
import os
import sys

import pdfplumber

DIR = os.path.join("testdata", "golden")


def main() -> int:
    target = DIR if len(sys.argv) < 2 else sys.argv[1]
    targets = sorted(
        f[: -len(".tables-text.target")]
        for f in os.listdir(target)
        if f.endswith(".tables-text.target")
    )
    if not targets:
        print(
            f"no .tables-text.target files in {target}. "
            "Create one alongside each fixture you want in the "
            "text-strategy parity set (the file can be empty; its "
            "presence is the signal).",
            file=sys.stderr,
        )
        return 1
    for name in targets:
        pdf_path = os.path.join(target, f"{name}.pdf")
        if not os.path.exists(pdf_path):
            print(f"missing {pdf_path}", file=sys.stderr)
            continue
        out = {"name": name, "pages": []}
        with pdfplumber.open(pdf_path) as pdf:
            for p in pdf.pages:
                tbls = p.find_tables(
                    {
                        "vertical_strategy": "text",
                        "horizontal_strategy": "text",
                        "min_words_vertical": 3,
                        "min_words_horizontal": 1,
                    }
                )
                page_obj = {
                    "number": p.page_number,
                    "width": p.width,
                    "height": p.height,
                    "tables": [t.extract() for t in tbls],
                }
                out["pages"].append(page_obj)
        outpath = os.path.join(target, f"{name}.tables-text.expected.json")
        with open(outpath, "w", encoding="utf-8") as f:
            json.dump(out, f, ensure_ascii=False, indent=2)
        ntables = sum(len(pp["tables"]) for pp in out["pages"])
        print(f"wrote {outpath}: {ntables} tables across all pages")
    return 0


if __name__ == "__main__":
    sys.exit(main())
