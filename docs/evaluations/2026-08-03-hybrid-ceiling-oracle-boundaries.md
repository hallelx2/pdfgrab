# The hybrid ceiling — pdfgrab with perfect table boundaries

**Date:** 2026-08-03
**Commit:** `0b39b95`
**Harness:** [`bench/icdar2013/oracle.py`](../../bench/icdar2013/oracle.py)
**Question:** if a layout model supplied correct rows and columns, how good would extraction be? That number decides whether the model is worth deploying.

## Result

| system | precision | recall | F1 |
| --- | --- | --- | --- |
| pdfgrab, current best (`lines`) | 0.865 | 0.229 | **0.362** |
| **pdfgrab + ORACLE boundaries** | **0.948** | **0.922** | **0.935** |
| pdfgrab + oracle + `MergeSplitTokens` | 0.806 | 0.660 | 0.726 |

107 documents (single-region pages).

## What it means

**0.362 → 0.935.** Given the right grid, pdfgrab extracts almost perfectly.

So essentially the entire gap in the end-to-end score is **table structure**, not text extraction. The cell-filling, the coordinates, the text fidelity — all the work of the last two days — is not the limiting factor. Finding the rows and columns is.

That is a clear verdict on the hybrid: **a layout model that outputs row/column structure is worth deploying.** It converts almost the whole gap.

It also confirms the division of labour. pdfgrab keeps what a generative model cannot give: exact cell text and exact coordinates for citation highlighting. The model supplies only geometry.

## `MergeSplitTokens` must be OFF when boundaries come from a model

0.935 → 0.726 with it on. That is not a small regression and the reason is structural: the setting exists to repair columns that a *geometric* boundary guess cut through a value. Given a correct grid there is nothing to repair, so every merge it performs destroys a correct cell.

**Rule: explicit boundaries from a model ⇒ `MergeSplitTokens = false`.** It remains useful for the pure-geometry path where boundaries are inferred.

## Integration point — already present

No new dependency and no HTTP client in the library:

```go
s := pdfgrab.DefaultTableSettings()
s.VerticalStrategy   = pdfgrab.StrategyExplicit
s.HorizontalStrategy = pdfgrab.StrategyExplicit
s.ExplicitVerticalLines   = colBoundaries // from the layout model
s.ExplicitHorizontalLines = rowBoundaries
s.MergeSplitTokens = false                // see above
tables, _ := page.ExtractTables(s)
```

The caller owns the model call. pdfgrab stays deterministic and offline.

## Two harness bugs, both worth recording

The first version of this experiment reported **0.119 F1 with perfect input** — near-random, and obviously measuring itself rather than the extractor. Two causes, and both are the same mistake in different clothes:

1. **Every cell bounding-box edge was treated as a grid line.** Cells in adjacent rows have slightly different extents, so this produced dozens of near-duplicate boundaries and shredded each table into fragments. Fixed by deriving the grid from the ground truth's own logical `start-col`/`end-col` indices: a column's extent is the span of the cells that begin and end in it, and the boundary between two columns is the midpoint of the gutter. That yields exactly `ncols+1` lines.
2. **Ground truth was counted for pages the oracle never attempted.** Boundaries were only supplied for single-region pages, but recall was scored against every region in the document — reporting an exclusion the experiment had chosen as an extraction failure. Fixed by restricting ground truth to the same pages.

Fixing (1) moved 0.119 → 0.782; fixing (2) moved 0.782 → 0.935.

A third suspicion turned out to be unfounded: the ground-truth Y origin was checked against pdfplumber word positions and is bottom-left, the same space pdfgrab reports (GT `y1=619.0` vs word `y0=616.9` on `eu-002` — a box-versus-glyph difference, not a flip). No conversion needed.

**The lesson is the same one from the font work:** a measurement that disagrees violently with expectation is far more likely to be a broken measurement than a broken system. Both times, checking the harness against a known-good case found the fault in the harness.

## Scope

Single-region pages only — 107 of 125 documents. Pages carrying several tables are excluded because merging their regions into one edge set produces a grid spanning the gap between them, which measures the harness again. A real layout model would emit one region per table, so this is a fair proxy for the hybrid, but it is not a measurement of multi-table pages.

## Reproduce

```sh
python bench/icdar2013/oracle.py ~/.cache/pdfgrab-bench/ICDAR-2013-Table-Competition-Corrected <extractor>
```
