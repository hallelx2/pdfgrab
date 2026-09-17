"""Score every available table-extraction system on ICDAR 2013.

    python bench/icdar2013/compare.py <corpus-root> <pdfgrab-extractor> [--limit N]

Where score.py answers "is pdfgrab as good as pdfplumber", this answers
the broader question: how does it stand against the field. Same corpus,
same metric, same process — the only thing that varies is the extractor.

Reports accuracy AND wall-clock. A benchmark that reports only F1 hides
the trade a pipeline actually has to make.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from collections import Counter

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from score import gt_relations, norm, prf, relations_from_grid, score  # noqa: E402
from systems import Timing, build_adapters, timed  # noqa: E402


def relations(tables) -> Counter:
    rels: Counter = Counter()
    for grid in tables:
        rels += relations_from_grid([[norm(c) for c in row] for row in grid])
    return rels


def find_pairs(root: str, limit: int = 0) -> list[tuple[str, str]]:
    pairs = []
    for dirpath, _, files in os.walk(root):
        for f in sorted(files):
            if not f.endswith("-str.xml"):
                continue
            pdf = os.path.join(dirpath, f.replace("-str.xml", ".pdf"))
            if os.path.exists(pdf):
                pairs.append((pdf, os.path.join(dirpath, f)))
    pairs.sort()
    return pairs[:limit] if limit else pairs


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("root", help="corpus root")
    ap.add_argument("exe", help="built pdfgrab extractor")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--gxpdf", default="", help="built coregx/gxpdf extractor")
    ap.add_argument("--json", default="", help="also write results here")
    args = ap.parse_args()

    pairs = find_pairs(args.root, args.limit)
    adapters = build_adapters(args.exe, args.gxpdf)

    active, skipped = [], []
    for a in adapters:
        ok, why = a.available()
        (active if ok else skipped).append((a, why))

    print(f"corpus     : {args.root}")
    print(f"documents  : {len(pairs)}")
    print(f"systems    : {len(active)} active, {len(skipped)} skipped\n")

    if skipped:
        print("skipped (library not importable):")
        for a, why in skipped:
            print(f"  {a.name:<26} pip install {a.install}")
            print(f"  {'':<26}   {why}")
        print()

    # Two aggregations, because they are different numbers and only one of
    # them is the competition's.
    #
    #   micro — pool every relation across the corpus, then score once.
    #           Weights a document by how many relations it has.
    #   macro — score each document, then average the per-document F1s.
    #           A one-table document counts as much as a five-table one.
    #
    # ICDAR 2013 specifies MACRO ("per-document averages"), so that is the
    # number comparable to published results. Micro is reported alongside
    # because it is the more natural read of "how many relations did we get
    # right", and quoting one while the reader assumes the other is exactly
    # how benchmark numbers get misused.
    totals = {a.name: [0, 0, 0] for a, _ in active}
    per_doc = {a.name: [] for a, _ in active}
    timings = {a.name: Timing() for a, _ in active}

    for i, (pdf, xml) in enumerate(pairs, 1):
        gt = gt_relations(xml)
        for a, _ in active:
            got = relations(timed(a.extract, pdf, timings[a.name]))
            c, nd, ng = score(gt, got)
            totals[a.name][0] += c
            totals[a.name][1] += nd
            totals[a.name][2] += ng
            per_doc[a.name].append(prf(c, nd, ng))
        if i % 10 == 0:
            print(f"  ...{i}/{len(pairs)}", flush=True)

    rows = []
    for a, _ in active:
        mp, mr, mf = prf(*totals[a.name])
        docs = per_doc[a.name]
        n = len(docs) or 1
        Mp = sum(d[0] for d in docs) / n
        Mr = sum(d[1] for d in docs) / n
        Mf = sum(d[2] for d in docs) / n
        t = timings[a.name]
        rows.append({
            "system": a.name,
            "version": a.version(),
            "macro_precision": round(Mp, 3),
            "macro_recall": round(Mr, 3),
            "macro_f1": round(Mf, 3),
            "micro_precision": round(mp, 3),
            "micro_recall": round(mr, 3),
            "micro_f1": round(mf, 3),
            "mean_ms": round(t.mean_ms(), 1),
            "p95_ms": round(t.p95_ms(), 1),
            "failures": t.failures,
            "note": a.note,
        })
    rows.sort(key=lambda d: d["macro_f1"], reverse=True)

    w = max(len(r["system"]) for r in rows) + 2
    print(f"\n{'':<{w}} {'--- per-document (ICDAR) ---':^25} {'-- pooled --':^17}")
    print(f"{'system':<{w}} {'prec':>7} {'recall':>7} {'F1':>8} "
          f"{'prec':>7} {'F1':>8} {'ms/doc':>9} {'p95 ms':>9} {'fails':>6}")
    print("-" * (w + 60))
    for r in rows:
        print(f"{r['system']:<{w}} {r['macro_precision']:>7.3f} "
              f"{r['macro_recall']:>7.3f} {r['macro_f1']:>8.3f} "
              f"{r['micro_precision']:>7.3f} {r['micro_f1']:>8.3f} "
              f"{r['mean_ms']:>9.1f} {r['p95_ms']:>9.1f} {r['failures']:>6d}")

    gtn = next(iter(totals.values()))[2] if totals else 0
    print(f"\nground-truth relations: {gtn}")
    print("\nMetric: adjacency relations (Goebel et al.), END-TO-END — find the")
    print("table AND grid it. NOT comparable to published structure-only scores,")
    print("which are handed the table region.")
    print()
    print("Ranked on PER-DOCUMENT F1, which is the ICDAR 2013 protocol and the")
    print("column to cite. Pooled F1 is shown too because it answers a different")
    print("question (how many relations were right overall) and the two diverge")
    print("whenever documents differ in size.")

    if args.json:
        with open(args.json, "w") as fh:
            json.dump({"documents": len(pairs), "results": rows}, fh, indent=2)
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
