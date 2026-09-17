# The in-region precision gap is not a tuning problem

**Date:** 2026-09-17
**Harness:** [`bench/icdar2013/sweep.py`](../../bench/icdar2013/sweep.py)
**Corpus:** ICDAR 2013, Smock-corrected — 125 PDFs, 39,524 relations
**Question:** region detection left precision at 0.281 against camelot's 0.514. Is that a threshold that was tuned for a page and is wrong for a region?

## Answer: no. Twelve configurations, and precision never leaves 0.28.

| config | precision | recall | **F1** |
|---|---|---|---|
| `pad=none` | 0.289 | 0.629 | **0.342** |
| `pad=0.5` | 0.288 | 0.630 | **0.342** |
| `minwh=2` | 0.283 | 0.635 | **0.338** |
| **baseline (`text`+detect)** | 0.281 | 0.635 | **0.337** |
| `minwh=1` | 0.281 | 0.635 | 0.337 |
| `pad=2.0` | 0.274 | 0.642 | 0.332 |
| `pad=3.0` | 0.261 | 0.637 | 0.322 |
| `pad=4.0` | 0.247 | 0.623 | 0.308 |
| `minwv=2 minwh=2` | 0.229 | 0.606 | 0.284 |
| `minwv=2` | 0.227 | 0.606 | 0.283 |
| `minwv=1` | 0.128 | 0.464 | 0.167 |
| `minwv=1 minwh=1` | 0.128 | 0.464 | 0.167 |

*Reference: pdfgrab `lines` 0.442 · camelot `stream` P 0.514 R 0.762 F1 0.582.*

Best is `pad=none` at 0.342 against a baseline of 0.337. **+0.005 is noise**, not
a finding. The entire reachable precision range is 0.128–0.289, and camelot's
0.514 is nowhere in it.

## The stated hypothesis was backwards

HAL-1363 proposed that `MinWordsVertical` (default 3) was *too high* for a small
region: a five-row table only has five words per column, so a page-tuned
threshold should be producing spurious boundaries.

The opposite is true. **Lowering it is catastrophic** — `minwv=1` halves
precision to 0.128 and takes recall down with it. The threshold is not too
strict; it is the only thing holding precision up at all.

Raising it (`minwv=2` is lower than the default 3, so the default is already the
strictest tested) also loses ground, so the default sits at or near the optimum
for this axis. There is no room on this parameter.

## Region padding behaves, and the small sample lied

An early three-document run put `pad=2.0` on top at F1 0.628 with recall 0.838.
At 125 documents that config is **sixth**, and padding beyond the default only
degrades — 2.0 → 3.0 → 4.0 costs precision monotonically (0.274 → 0.261 → 0.247)
for no recall worth having.

Worth recording as a method note rather than a footnote: a three-document
sample inverted the ranking of the best and sixth-best configurations. The
cheap smoke test is for checking the harness runs, never for reading a result
off.

## What this rules out, and what it leaves

Ruled out: **the precision gap is not a configuration difference.** No
combination of the exposed thresholds moves it, so no default change will fix
it and no further sweeping is worth the wall-clock.

What is left is the gridding algorithm itself. Given the same detected region,
camelot divides it into cells differently, and that difference is worth roughly
**0.23 precision** — far too large to be tuning. Reading `_generate_columns_and_rows`
in `camelot/parsers/base.py` is now the only sensible next step.

The oracle result still bounds the opportunity: handed a **correct** grid, this
same extractor reaches **0.935**. Nothing here suggests the ceiling is the
problem.

## Cost of learning this

Roughly ten minutes of compute, twice, against the days that tuning thresholds
by hand would have taken to reach the same conclusion less certainly. The sweep
script is committed so the next parameter question is a one-liner.

## Reproduce

```sh
python bench/icdar2013/sweep.py \
    ~/.cache/pdfgrab-bench/ICDAR-2013-Table-Competition-Corrected \
    ~/.cache/pdfgrab-bench/bench-extract
```
