"""Measure the CEILING of a layout-model hybrid.

Feeds pdfgrab the ground-truth row and column boundaries — the output a
perfect layout model would produce — and scores what comes back. pdfgrab
then does only the part it is good at: filling cells from the text layer
and reporting exact coordinates.

This sizes the hybrid before anyone builds it. If extraction with perfect
boundaries scores near 1.0, every remaining point is a detection/structure
problem and a layout model buys all of it. If it scores 0.6, the ceiling
is far below the pitch and the geometry needs work first.

    python bench/icdar2013/oracle.py <dataset-root> <extractor-exe> [limit]

## Deriving the grid

The obvious approach — collect every cell bounding-box edge and use them
all — does NOT work. Cells in different rows have slightly different
extents, so it yields dozens of near-duplicate boundaries and shreds the
table into fragments. The first version of this scored 0.119 F1 with
PERFECT input, i.e. it measured itself.

Instead the grid comes from the ground truth's own logical indices. Each
cell carries start-col/end-col and start-row/end-row, so column c's extent
is the span of the cells that begin and end in it, and the boundary
between adjacent columns is the midpoint of the gap between them. That
yields exactly ncols+1 lines, which is what a grid actually is.

Ground-truth boxes are in PDF points with the origin bottom-left — the
same space pdfgrab reports — verified against pdfplumber word positions
(GT y1=619.0 vs word y0=616.9 on eu-002; a box-vs-glyph difference, not a
flip). No conversion is needed.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from collections import Counter

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from score import norm, prf, relations_from_grid, score  # noqa: E402


def _boundaries(lo: dict[int, float], hi: dict[int, float]) -> list[float]:
    """Grid lines from per-index extents: outer edges plus gap midpoints."""
    idx = sorted(set(lo) | set(hi))
    if len(idx) < 1:
        return []
    out = [lo[idx[0]]]
    for a, b in zip(idx, idx[1:]):
        # Midpoint of the gutter between two adjacent bands. Using either
        # edge alone would clip whichever side is wider on some rows.
        out.append((hi[a] + lo[b]) / 2)
    out.append(hi[idx[-1]])
    return sorted(out)


def oracle_edges(xml_path: str) -> dict[str, dict[str, list[float]]]:
    root = ET.parse(xml_path).getroot()

    # One region per page only. Merging several tables on a page into one
    # edge set produces a mega-grid spanning the gap between them.
    per_page: Counter = Counter(r.get("page", "1") for r in root.iter("region"))

    pages: dict[str, dict[str, list[float]]] = {}
    for region in root.iter("region"):
        page = region.get("page", "1")
        if per_page[page] != 1:
            continue

        col_lo: dict[int, float] = {}
        col_hi: dict[int, float] = {}
        row_lo: dict[int, float] = {}
        row_hi: dict[int, float] = {}

        for cell in region.findall("cell"):
            bb = cell.find("bounding-box")
            if bb is None:
                continue
            x1, x2 = float(bb.get("x1")), float(bb.get("x2"))
            y1, y2 = float(bb.get("y1")), float(bb.get("y2"))
            sc = int(cell.get("start-col", 0))
            ec = int(cell.get("end-col", sc))
            sr = int(cell.get("start-row", 0))
            er = int(cell.get("end-row", sr))

            # Only cells that BEGIN in a band define its near edge, and
            # only cells that END in it define the far edge — a spanning
            # cell says nothing about the bands it merely crosses.
            col_lo[sc] = min(col_lo.get(sc, x1), x1)
            col_hi[ec] = max(col_hi.get(ec, x2), x2)

            # Row index 0 is the TOP row, and y grows upward, so row order
            # is the reverse of y order. Negate to keep the index-ordered
            # helper honest, then flip back.
            row_lo[sr] = min(row_lo.get(sr, -y2), -y2)
            row_hi[er] = max(row_hi.get(er, -y1), -y1)

        v = _boundaries(col_lo, col_hi)
        h = sorted(-y for y in _boundaries(row_lo, row_hi))
        if len(v) >= 2 and len(h) >= 2:
            pages[page] = {"v": v, "h": h}
    return pages


def gt_relations_single_region(xml_path: str) -> Counter:
    """Ground truth restricted to the pages the oracle actually covers.

    Scoring against ALL regions while only feeding boundaries for
    single-region pages deflates recall with tables the experiment never
    attempted — it would report the multi-region exclusion as an
    extraction failure. Same class of mistake as the edge-clustering bug
    above: the harness measuring itself.
    """
    rels: Counter = Counter()
    root = ET.parse(xml_path).getroot()
    per_page: Counter = Counter(r.get("page", "1") for r in root.iter("region"))
    for region in root.iter("region"):
        if per_page[region.get("page", "1")] != 1:
            continue
        cells = []
        for cell in region.findall("cell"):
            sr = int(cell.get("start-row", 0))
            sc = int(cell.get("start-col", 0))
            er = int(cell.get("end-row", sr))
            ec = int(cell.get("end-col", sc))
            content = cell.find("content")
            cells.append((sr, sc, er, ec,
                          norm(content.text if content is not None else "")))
        if not cells:
            continue
        nrows = max(c[2] for c in cells) + 1
        ncols = max(c[3] for c in cells) + 1
        grid = [["" for _ in range(ncols)] for _ in range(nrows)]
        for sr, sc, er, ec, text in cells:
            for r in range(sr, er + 1):
                for c in range(sc, ec + 1):
                    if r < nrows and c < ncols:
                        grid[r][c] = text
        rels += relations_from_grid(grid)
    return rels


def main() -> int:
    root, exe = sys.argv[1], sys.argv[2]
    limit = int(sys.argv[3]) if len(sys.argv) > 3 else 0

    pairs = []
    for dirpath, _, files in os.walk(root):
        for f in sorted(files):
            if f.endswith("-str.xml"):
                pdf = os.path.join(dirpath, f.replace("-str.xml", ".pdf"))
                if os.path.exists(pdf):
                    pairs.append((pdf, os.path.join(dirpath, f)))
    pairs.sort()
    if limit:
        pairs = pairs[:limit]

    # Scored over the pages the oracle can actually describe (single-region
    # pages). Documents with none contribute nothing either way, so the
    # number is not diluted by pages the experiment says nothing about.
    print(f"scoring {len(pairs)} documents with ORACLE boundaries\n", flush=True)

    variants = {"oracle": [], "oracle + MergeSplitTokens": ["-merge"]}
    totals = {k: [0, 0, 0] for k in variants}
    scored_docs = 0

    for i, (pdf, xml) in enumerate(pairs, 1):
        edges = oracle_edges(xml)
        if not edges:
            continue
        scored_docs += 1
        gt = gt_relations_single_region(xml)
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
            json.dump(edges, fh)
            path = fh.name
        try:
            for name, extra in variants.items():
                try:
                    out = subprocess.run(
                        [exe, "-oracle", path] + extra + [pdf],
                        capture_output=True, timeout=180).stdout
                    tables = json.loads(out or b"[]")
                except Exception:
                    tables = []
                rels: Counter = Counter()
                for t in tables:
                    rels += relations_from_grid(
                        [[norm(c) for c in r] for r in t["rows"]])
                c, nd, ng = score(gt, rels)
                totals[name][0] += c
                totals[name][1] += nd
                totals[name][2] += ng
        finally:
            os.unlink(path)
        if i % 20 == 0:
            print(f"  ...{i}/{len(pairs)}", flush=True)

    print(f"\n{'system':<40} {'precision':>10} {'recall':>10} {'F1':>10}")
    print("-" * 74)
    for name in variants:
        p, r, f = prf(*totals[name])
        print(f"{'pdfgrab + ' + name:<40} {p:>10.3f} {r:>10.3f} {f:>10.3f}")
    print(f"\ndocuments scored: {scored_docs} (single-region pages only)")
    print("This is the ceiling a perfect layout model could hand pdfgrab.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
