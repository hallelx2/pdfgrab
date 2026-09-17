# Text-edge region detection — the detector works, the gridding does not

**Date:** 2026-09-17
**Harness:** [`bench/icdar2013/compare.py`](../../bench/icdar2013/compare.py)
**Corpus:** ICDAR 2013, Smock-corrected — 125 PDFs, 39,524 relations
**Question:** camelot reaches 0.762 recall with Nurminen's text-edge detector and we reach 0.422. Does porting it close the gap?

## Result: partial. Half the gap closes, and the half that does not is somewhere else.

| Configuration | precision | recall | **F1** | ms/doc |
|---|---|---|---|---|
| pdfgrab `text`, page-wide *(control)* | 0.187 | 0.547 | **0.245** | 288 |
| **pdfgrab `text` + region detection** | **0.281** | **0.635** | **0.337** | 227 |
| pdfgrab `fallback` + region detection | 0.336 | 0.575 | 0.324 | 194 |
| pdfgrab `lines` *(current default)* | 0.545 | 0.422 | 0.442 | 62 |
| camelot `stream` *(the target)* | 0.514 | 0.762 | 0.582 | 275 |

Against its own control the detector is a clear win: **+0.094 precision, +0.088
recall, F1 0.245 → 0.337**, a 38% relative gain, and *faster* — 288ms → 227ms,
because gridding a few bounded regions is less work than gridding a whole page.

Against `lines` it is still a loss (0.337 vs 0.442), so **it does not become the
default**. It ships opt-in behind `TableSettings.DetectRegions`.

## What the numbers say about where the remaining gap is

Comparing the ported detector against camelot, which runs the same algorithm:

| | recall | precision |
|---|---|---|
| pdfgrab + detection | 0.635 | **0.281** |
| camelot `stream` | 0.762 | **0.514** |

**Recall came across; precision did not.** 0.635 against 0.762 is the same
regime — the detector is finding broadly the right regions. 0.281 against 0.514
is not a difference of degree.

That localises the problem, because **recall is a detection property and
precision, once you have a region, is a gridding property.** Given the same
bounded area, the two systems divide it into cells differently, and ours emits
many more wrong ones.

So the conclusion is not "Nurminen's method does not transfer". It is: the
detection half transferred, and pdfgrab's cell inference *inside* a known region
is now the bottleneck. That is a different problem from the one this work set
out to solve, and a more tractable one — the oracle experiment already showed
that given a **correct grid** the same extractor reaches 0.935.

## Why it beats the page-wide text strategy

The control and the treatment run identical cell inference. The only difference
is that one sees the whole page and the other sees detected regions. Precision
rises by half again (0.187 → 0.281) purely from not gridding prose.

That is the mechanism working exactly as described: the ≥4-vertically-adjacent-
lines rule is a filter prose cannot pass, so the pathology that makes the bare
`text` strategy unusable — inventing a table on every page — is substantially
suppressed.

Recall rising too (0.547 → 0.635) is the less obvious half. Restricting
attention to a region makes the column inference *better*, not merely safer: a
cluster threshold that is right for a table is wrong for a page, so page-wide
derivation was also missing real columns.

## Why it is not the default

`StrategyAuto` is the precedent. It also found more regions, and it scored
slightly *worse* (F1 0.362 → 0.358 pooled) because regions found but gridded
badly cost more precision than they buy in recall. The same shape appears here,
just with a larger win on the other side.

`lines` remains the default because 0.442 > 0.337. Region detection is worth
turning on when a corpus is known to contain unruled tables — where `lines`
returns nothing at all and 0.337 beats zero.

## Determinism

The detector has its own determinism test (`TestIsDeterministic`, 25 runs over a
two-table page, exact bbox equality), and the full suite was re-run end to end;
the 2026-09-17 field comparison established that the harness reproduces to the
decimal.

## What to do next

Gridding inside a known region, not detection. Specifically:

- pdfgrab's column inference requires `MinWordsVertical` (default 3) words to
  agree before emitting a boundary. That threshold was tuned for a whole page.
  Inside a small region it is probably wrong, in the direction that produces
  spurious columns.
- camelot grids with `row_tol=2`, `column_tol=0` and its own cell logic. That is
  the next thing to read.
- The oracle result (0.935 given a correct grid) bounds what is available here:
  the extractor is not the limit.

## Reproduce

```sh
go build -o bench-extract ./bench/icdar2013   # or via run.py
bench-extract -strategy text -detect file.pdf
```

In code:

```go
s := pdfgrab.DefaultTableSettings()
s.VerticalStrategy = pdfgrab.StrategyText
s.HorizontalStrategy = pdfgrab.StrategyText
s.DetectRegions = true
tables, _ := page.ExtractTables(s)
```
