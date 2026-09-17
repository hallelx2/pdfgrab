# ICDAR 2013 — table detection and structure

**Date:** 2026-08-02
**Commit:** `0ca65ca` (after the font-metric and table-fidelity work of HAL-480/481/510/520/548/511)
**Harness:** [`bench/icdar2013`](../../bench/icdar2013/) — `python bench/icdar2013/run.py`
**Dataset:** ICDAR 2013 Table Competition, Smock's corrected edition — 125 born-digital PDFs, 39,524 ground-truth adjacency relations
**Reference:** pdfplumber 0.11.9

## Result

| system | precision | recall | F1 |
| --- | --- | --- | --- |
| pdfgrab (`lines`) | 0.865 | 0.229 | **0.362** |
| pdfplumber (`lines`) | 0.868 | 0.235 | **0.370** |
| pdfgrab (fallback `lines`→`text`) | 0.223 | 0.557 | 0.318 |
| pdfgrab (fallback + `MergeSplitTokens`) | 0.471 | 0.275 | 0.347 |
| pdfplumber (`text`) | 0.167 | 0.679 | 0.267 |

## Two findings

### 1. Parity holds at the table level

0.362 vs 0.370, with near-identical precision (0.865 vs 0.868). Everything
before this validated *text* fidelity — word positions, glyph widths,
signs. This is the first measurement of *table* behaviour, and pdfgrab
tracks its reference implementation there too.

### 2. The bottleneck is detection, not structure

```
documents                          : 125
documents where NO table was found : 28  (22%)
tables detected (total)            : 306

restricted to documents where a table WAS detected:
  precision 0.865   recall 0.409   F1 0.556
```

Precision 0.865 says **what we extract is right**. Recall 0.229 says **we
miss three quarters of it**. Those are very different problems and they
need opposite work.

The cause is structural. The `lines` strategy builds cells from
**intersecting** rulings. A table ruled only horizontally — booktabs
style, ubiquitous in government and academic documents — yields no
intersections and is invisible. Confirmed on `us-018.pdf`: 46 ruling lines
on page 1, zero rectangles, zero tables detected.

The `text` strategy sees those tables (recall 0.56–0.68) but fabricates
tables on prose pages (precision 0.17–0.22). Neither library has a usable
"is this actually a table?" decision, which is why the naive fallback
scores *worse* overall than `lines` alone.

## Caveat on the number

**0.36 is not comparable to the 0.85–0.95 reported in table-structure
papers.** Those benchmarks hand the system the table region and score only
the gridding. This runs the harder end-to-end task. The 0.556 figure on
documents where detection succeeded is closer to a structure-only
comparison, and still contains intra-document detection misses.

The honest one-line summary: **cell-level extraction is strong, table
detection is weak.**

## What this implies

Detection — *where is the table* — is a vision problem, and the thing
CV/VLM models are built for. Content fidelity — the values, signs and
coordinates — is a text-layer problem, where the deterministic parser is
provably better (it preserves accounting minus signs that pdfplumber
drops; see the [font-metrics evaluation](2026-08-02-font-metrics-and-table-fidelity.md)).

That argues for splitting the work by which half is weak, rather than
replacing either:

- **layout/VLM model → table region and row/column structure** (attacks
  the 0.229 recall)
- **pdfgrab text layer → cell contents and coordinates** (keeps exact
  values and citation geometry, which a generative model cannot provide)

Two cheaper wins should be tried first, since that is where the points
are:

1. Support horizontally-ruled-only tables — derive column edges from word
   alignment *within* the rule band instead of requiring vertical rulings.
2. Give the `text` strategy a confidence signal, so a fallback can reject
   prose instead of emitting a table for every page.

Tracked as HAL-568. Re-run this harness after each and record a new dated
report here.
