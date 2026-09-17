"""Benchmark table extraction on the ICDAR 2013 table competition set.

Metric: adjacency relations (Goebel et al., ICDAR 2013) — the standard for
this dataset. For every non-empty cell, take its nearest non-empty
neighbour to the right and below; each such pair is a relation
(text_a, text_b, direction). Score the detected relation multiset against
ground truth.

Why this metric rather than comparing grids cell-by-cell: two tools can
grid a table differently — one emits a spacer column, another does not —
and still convey exactly the same structure. Adjacency asks the question
that actually matters for a reader: "is this value next to that label?"
It is unforgiving about genuinely wrong structure and forgiving about
harmless disagreements over gridding.

Documents are scored as a whole (relations unioned across pages/tables),
which sidesteps having to match detected tables to ground-truth tables —
a matching step that would itself need arbitrary thresholds.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import xml.etree.ElementTree as ET
from collections import Counter

WS = re.compile(r"\s+")


def norm(s: str | None) -> str:
    return WS.sub(" ", (s or "")).strip()


def relations_from_grid(grid: list[list[str]]) -> Counter:
    """Adjacency relations from a dense row/col grid."""
    rels: Counter = Counter()
    nrows = len(grid)
    for r in range(nrows):
        ncols = len(grid[r])
        for c in range(ncols):
            a = grid[r][c]
            if not a:
                continue
            # nearest non-empty to the right, skipping blanks
            for c2 in range(c + 1, ncols):
                if grid[r][c2]:
                    rels[(a, grid[r][c2], "H")] += 1
                    break
            # nearest non-empty below
            for r2 in range(r + 1, nrows):
                row2 = grid[r2]
                if c < len(row2) and row2[c]:
                    rels[(a, row2[c], "V")] += 1
                    break
    return rels


def gt_relations(xml_path: str) -> Counter:
    """Ground truth: build a dense grid per region from start/end row+col."""
    rels: Counter = Counter()
    root = ET.parse(xml_path).getroot()
    for region in root.iter("region"):
        cells = []
        for cell in region.findall("cell"):
            sr = int(cell.get("start-row", 0))
            sc = int(cell.get("start-col", 0))
            er = int(cell.get("end-row", sr))
            ec = int(cell.get("end-col", sc))
            content = cell.find("content")
            text = norm(content.text if content is not None else "")
            cells.append((sr, sc, er, ec, text))
        if not cells:
            continue
        nrows = max(c[2] for c in cells) + 1
        ncols = max(c[3] for c in cells) + 1
        grid = [["" for _ in range(ncols)] for _ in range(nrows)]
        for sr, sc, er, ec, text in cells:
            # A spanning cell occupies every position it covers, which is
            # what makes adjacency work across merged headers.
            for r in range(sr, er + 1):
                for c in range(sc, ec + 1):
                    if r < nrows and c < ncols:
                        grid[r][c] = text
        rels += relations_from_grid(grid)
    return rels


def score(gt: Counter, got: Counter) -> tuple[int, int, int]:
    correct = sum((gt & got).values())
    return correct, sum(got.values()), sum(gt.values())


def prf(correct: int, ndet: int, ngt: int) -> tuple[float, float, float]:
    p = correct / ndet if ndet else 0.0
    r = correct / ngt if ngt else 0.0
    f = 2 * p * r / (p + r) if (p + r) else 0.0
    return p, r, f


def run_pdfgrab(exe: str, pdf: str, strategy: str, merge: bool) -> Counter:
    cmd = [exe, "-strategy", strategy]
    if merge:
        cmd.append("-merge")
    cmd.append(pdf)
    try:
        out = subprocess.run(cmd, capture_output=True, timeout=120).stdout
        tables = json.loads(out or b"[]")
    except Exception:
        return Counter()
    rels: Counter = Counter()
    for t in tables:
        rels += relations_from_grid([[norm(c) for c in row] for row in t["rows"]])
    return rels


def run_pdfplumber(pdf: str, strategy: str) -> Counter:
    import pdfplumber

    settings = {"vertical_strategy": strategy, "horizontal_strategy": strategy}
    rels: Counter = Counter()
    try:
        with pdfplumber.open(pdf) as doc:
            for page in doc.pages:
                for tbl in page.extract_tables(settings):
                    rels += relations_from_grid(
                        [[norm(c) for c in row] for row in tbl]
                    )
    except Exception:
        return Counter()
    return rels


def main() -> int:
    root = sys.argv[1]
    exe = sys.argv[2]
    limit = int(sys.argv[3]) if len(sys.argv) > 3 else 0

    pairs = []
    for dirpath, _, files in os.walk(root):
        for f in sorted(files):
            if not f.endswith("-str.xml"):
                continue
            pdf = os.path.join(dirpath, f.replace("-str.xml", ".pdf"))
            if os.path.exists(pdf):
                pairs.append((pdf, os.path.join(dirpath, f)))
    pairs.sort()
    if limit:
        pairs = pairs[:limit]
    print(f"scoring {len(pairs)} documents\n", flush=True)

    systems = {
        "pdfgrab (lines)": lambda p: run_pdfgrab(exe, p, "lines", False),
        "pdfgrab (AUTO)": lambda p: run_pdfgrab(exe, p, "auto", False),
        "pdfgrab (AUTO +merge)": lambda p: run_pdfgrab(exe, p, "auto", True),
        "pdfplumber (lines)": lambda p: run_pdfplumber(p, "lines"),
    }
    totals = {k: [0, 0, 0] for k in systems}

    for i, (pdf, xml) in enumerate(pairs, 1):
        gt = gt_relations(xml)
        for name, fn in systems.items():
            c, nd, ng = score(gt, fn(pdf))
            totals[name][0] += c
            totals[name][1] += nd
            totals[name][2] += ng
        if i % 10 == 0:
            print(f"  ...{i}/{len(pairs)}", flush=True)

    print(f"\n{'system':<28} {'precision':>10} {'recall':>10} {'F1':>10}")
    print("-" * 62)
    for name in systems:
        p, r, f = prf(*totals[name])
        print(f"{name:<28} {p:>10.3f} {r:>10.3f} {f:>10.3f}")
    print(f"\nground-truth relations: {totals['pdfgrab (lines)'][2]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
