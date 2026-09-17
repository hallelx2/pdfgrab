# StrategyAuto — improves detection, does not improve accuracy

**Date:** 2026-08-02
**Commit:** `05c0c92` + `StrategyAuto`
**Harness:** [`bench/icdar2013`](../../bench/icdar2013/)
**Verdict:** shipped as **opt-in**; does **not** become a default. The
hypothesis it tested is disproved.

## Hypothesis

The [ICDAR 2013 evaluation](2026-08-02-icdar2013-table-structure.md) found
28 of 125 documents (22%) where pdfgrab detected **no table at all**, and
attributed it to tables ruled on one axis only. `lines` builds cells from
*intersecting* rulings, so a horizontally-ruled table yields none.

Confirmed directly — every zero-detection document had horizontal rules
and zero vertical ones:

```
us-017:  218 H,   0 V     lines=0  mixed=6
us-018:  226 H,   0 V     lines=0  mixed=7
us-024:  135 H,   0 V     lines=0  mixed=4
us-025:  225 H,   0 V     lines=0  mixed=3
```

So: pick `lines` for the ruled axis and `text` for the unruled one, but
**only when the other axis is ruled** — those rulings being the evidence
that a table is genuinely present. Expectation: recall rises, precision
holds.

## Result — the hypothesis was wrong

| system | precision | recall | F1 |
| --- | --- | --- | --- |
| pdfgrab (`lines`) | 0.865 | 0.229 | **0.362** |
| pdfgrab (`auto`) | 0.797 | 0.231 | **0.358** |
| pdfgrab (`auto` + MergeSplitTokens) | 0.826 | 0.230 | 0.359 |
| pdfplumber (`lines`) | 0.868 | 0.235 | 0.370 |

Recall moved 0.229 → 0.231. Precision fell 0.865 → 0.797. **Net slightly
worse.**

## Why — and it corrects the earlier conclusion

Detection did improve, exactly as predicted:

| | `lines` | `auto` |
| --- | --- | --- |
| documents with no table found | 28 (22%) | **23 (18%)** |
| tables detected | 306 | **331** |
| F1 *on documents where one was found* | 0.556 | **0.400** |

We now find tables in five more documents. **The tables we find there are
gridded badly.** Once the newly-detected hard documents enter the scored
set, quality on that set falls from 0.556 to 0.400.

The earlier report concluded "the bottleneck is detection, not
structure", reasoning from precision 0.865 on detected documents. That
was true of the documents `lines` could already see — an easier
population, self-selected by being fully ruled. On the harder ones,
**structure is weak too**: knowing where the table is does not tell you
where its columns are, and word-alignment clustering does not recover
them on these layouts.

Corrected statement: **detection and structure are both weak on
one-axis-ruled tables. Fixing detection alone converts almost nothing.**

## What was shipped, and why anything at all

`StrategyAuto` is available per axis and is **not** a default. It is
correct for its stated case — it finds tables that `lines` cannot see —
and a caller who knows their corpus is booktabs-ruled gets a real
improvement. `TestAutoIsNotTheDefault` pins that it stays opt-in.

The conservative rule is load-bearing and worth keeping even though the
headline did not move. Auto declines to guess when *neither* axis is
ruled. Without that guard, a naive `lines`→`text` fallback scores 0.223
precision — prose has word alignment too, and the text strategy reports a
table for it. That is why the fallback row in the earlier report is worse
than `lines` alone.

## What this implies for the next attempt

Do not spend more effort on heuristics for finding the table region. The
measurement says the missing piece is **row and column structure** on
layouts where the rules do not supply it.

That is a different shape of problem, and it maps onto what layout models
actually output. Table Transformer and similar predict rows, columns and
spanning cells — not merely a table bounding box. That is the part
geometry cannot recover here.

Revised split for the hybrid:

- **layout/VLM model → rows, columns and spans** (not just "where is the
  table")
- **pdfgrab text layer → cell contents and coordinates**, which stays
  exact and keeps citation geometry

## Reproduce

```sh
python bench/icdar2013/run.py
DIAG_STRATEGY=auto python bench/icdar2013/diag.py <dataset> <extractor>
```
