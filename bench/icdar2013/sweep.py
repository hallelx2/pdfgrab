"""Sweep the in-region gridding thresholds (HAL-1363).

    python bench/icdar2013/sweep.py <corpus> <pdfgrab-extractor> [--limit N]

Region detection raised recall but left precision at 0.281 against
camelot's 0.514. The suspicion is configuration rather than algorithm:
MinWordsVertical/Horizontal were tuned for a whole page, and inside a
five-row region the statistics that made 3 sensible no longer hold.

This answers that before any algorithm work. If precision moves
materially on a threshold alone, the fix is a default; if it does not,
the gridding logic itself is the problem and this saves the effort of
finding that out the slow way.

Scores only pdfgrab configurations — the competitors do not change, and
re-running them would triple the wall-clock for no information.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from collections import Counter

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from compare import find_pairs, relations  # noqa: E402
from score import gt_relations, prf, score  # noqa: E402


def run_cfg(exe: str, pdf: str, cfg: dict) -> Counter:
    cmd = [exe, "-strategy", cfg["strategy"]]
    if cfg.get("detect"):
        cmd.append("-detect")
    if cfg.get("minwv"):
        cmd += ["-minwv", str(cfg["minwv"])]
    if cfg.get("minwh"):
        cmd += ["-minwh", str(cfg["minwh"])]
    if "pad" in cfg:
        cmd += ["-pad", str(cfg["pad"])]
    cmd.append(pdf)
    try:
        out = subprocess.run(cmd, capture_output=True, timeout=120).stdout
        return relations([t["rows"] for t in json.loads(out or b"[]")])
    except Exception:
        return Counter()


def configs() -> list[dict]:
    out = [{"name": "baseline text+detect", "strategy": "text", "detect": True}]

    # One axis at a time, so a move can be attributed.
    for n in (1, 2):
        out.append({"name": f"minwv={n}", "strategy": "text", "detect": True, "minwv": n})
        out.append({"name": f"minwh={n}", "strategy": "text", "detect": True, "minwh": n})

    # Both together, in case the effect only appears jointly.
    out.append({"name": "minwv=1 minwh=1", "strategy": "text", "detect": True,
                "minwv": 1, "minwh": 1})
    out.append({"name": "minwv=2 minwh=2", "strategy": "text", "detect": True,
                "minwv": 2, "minwh": 2})

    # Region padding: too much drags prose in, too little clips a row.
    for pad in (-1, 0.5, 2.0, 3.0, 4.0):
        label = "none" if pad < 0 else pad
        out.append({"name": f"pad={label}", "strategy": "text", "detect": True, "pad": pad})

    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("root")
    ap.add_argument("exe")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--json", default="")
    args = ap.parse_args()

    pairs = find_pairs(args.root, args.limit)
    cfgs = configs()
    print(f"documents  : {len(pairs)}")
    print(f"configs    : {len(cfgs)}\n", flush=True)

    per_doc = {c["name"]: [] for c in cfgs}

    for i, (pdf, xml) in enumerate(pairs, 1):
        gt = gt_relations(xml)
        for c in cfgs:
            got = run_cfg(args.exe, pdf, c)
            per_doc[c["name"]].append(prf(*score(gt, got)))
        if i % 10 == 0:
            print(f"  ...{i}/{len(pairs)}", flush=True)

    rows = []
    for c in cfgs:
        d = per_doc[c["name"]]
        n = len(d) or 1
        rows.append({
            "config": c["name"],
            "precision": round(sum(x[0] for x in d) / n, 3),
            "recall": round(sum(x[1] for x in d) / n, 3),
            "f1": round(sum(x[2] for x in d) / n, 3),
        })
    rows.sort(key=lambda r: r["f1"], reverse=True)

    w = max(len(r["config"]) for r in rows) + 2
    print(f"\n{'config':<{w}} {'prec':>7} {'recall':>7} {'F1':>7}")
    print("-" * (w + 24))
    for r in rows:
        print(f"{r['config']:<{w}} {r['precision']:>7.3f} {r['recall']:>7.3f} {r['f1']:>7.3f}")

    print("\nReference on this corpus: pdfgrab lines F1 0.442 · camelot stream")
    print("P 0.514 R 0.762 F1 0.582. Per-document averaging, end-to-end.")

    if args.json:
        with open(args.json, "w") as fh:
            json.dump({"documents": len(pairs), "results": rows}, fh, indent=2)
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
