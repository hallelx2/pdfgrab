# The field, and a metric we had been computing wrong

**Date:** 2026-09-17
**Harness:** [`bench/icdar2013/compare.py`](../../bench/icdar2013/compare.py) · [`systems.py`](../../bench/icdar2013/systems.py)
**Corpus:** ICDAR 2013 Table Competition, Smock-corrected edition — 125 PDFs, 39,524 ground-truth adjacency relations
**Questions:** where does pdfgrab sit against the whole field rather than against pdfplumber alone, and is any Go library better?

## Correction first: our number was not the competition's metric

Every pdfgrab figure published before today — the 0.362 that appears throughout
this repo — was computed by **pooling every adjacency relation across the whole
corpus and scoring once**. That is micro-averaging.

ICDAR 2013 does not do that. It computes precision/recall/F1 **per document and
averages over documents**. Evidence, in order of authority:

- The competition's own evaluator, `tamirhassan/dataset-tools` (Apache-2.0),
  prints per-table precision/recall and performs **no aggregation at all** — it
  emits the raw counts and leaves pooling to the caller.
- Namysl et al. (VISAPP 2022), reproducing the competition results, state the
  aggregation explicitly: "*We report the precision, recall, and F1 score
  (per-document averages) for the complete recognition process.*"

The two differ by a meaningful margin, because a document with one large table
and a document with six small ones count equally under one scheme and very
unequally under the other:

| | pooled *(what we published)* | per-document *(the competition's)* |
|---|---|---|
| pdfgrab (`lines`) | 0.362 | **0.442** |
| pdfplumber (`lines`) | 0.370 | **0.458** |

**The citable end-to-end figure for pdfgrab is 0.442, not 0.362.** The harness
now reports both and ranks on the per-document column.

The earlier evaluations are left as written. They record what was measured on
the day with the metric as it was then implemented; a note now points here.
Rewriting a dated measurement is worse than annotating it.

## Results

Ten systems, same corpus, same metric, same process. End-to-end: each system
must **find** the table and **grid** it.

| System | Version | per-doc P | per-doc R | **per-doc F1** | pooled F1 | ms/doc | p95 ms |
|---|---|---|---|---|---|---|---|
| camelot (stream) | 2.0.0 | 0.514 | 0.762 | **0.582** | 0.716 | 300 | 899 |
| PyMuPDF `find_tables` | 1.28.2 | 0.560 | 0.472 | **0.485** | 0.392 | 730 | 2116 |
| camelot (lattice) | 2.0.0 | 0.520 | 0.453 | **0.467** | 0.396 | 1554 | 3237 |
| pdfplumber (lines) | 0.11.10 | 0.558 | 0.440 | **0.458** | 0.370 | 794 | 2066 |
| **pdfgrab (auto)** | — | 0.548 | 0.422 | **0.443** | 0.358 | **86** | 274 |
| **pdfgrab (lines)** | — | 0.545 | 0.422 | **0.442** | 0.362 | **81** | 247 |
| tabula (stream) | 2.10.0 | 0.387 | 0.460 | **0.397** | 0.437 | 1100 | 2680 |
| tabula (lattice) | 2.10.0 | 0.258 | 0.339 | **0.257** | 0.107 | 164 | 415 |
| pdfplumber (text) | 0.11.10 | 0.188 | 0.559 | **0.248** | 0.267 | 1456 | 4043 |
| **gxpdf (Go)** | v0.9.4 | 0.201 | 0.176 | **0.179** | 0.293 | 73 | 213 |

No system recorded a failure on any document.

## What it says

### No Go library beats pdfgrab

`coregx/gxpdf` (MIT, pure Go, v0.9.4, 2026-08-02) is the only other
permissively-licensed Go library that extracts tables. It scores **0.179** —
last of ten, 2.5x behind pdfgrab.

The raw output shows why. On `eu-001.pdf` it returns cells like:

```
"N i t r og en ox id es (N Ox/N O2 )  100 00 0  -   -  \nHy dro g en  Cy anide..."
```

Whole text blocks merged into one cell, with the per-glyph spacing unresolved —
the class of error the AFM metrics work fixed here. Its README claims "100%
accuracy on bank statements", which is plausibly true and narrow: ruled
financial tables are the case `lattice`-style detection handles well.

For completeness, the Go field: `unidoc/unipdf` v5 has the best output of any
Go library — `TextTable` with per-cell bbox *and* grid indices — but is
**commercial-licence-only** since v5 (the AGPL option was removed), so it cannot
be benchmarked without a key and cannot be depended on by an MIT project.
`klippa-app/go-pdfium` gives per-character boxes and, notably, runs
**CGo-free in WebAssembly mode** — but has no table layer. `pdfcpu` has no text
extraction at all. `ledongthuc/pdf`, `rsc.io/pdf` and `dslipak/pdf` give text
positions but no tables.

### Against Python, pdfgrab is mid-pack — and that is the honest claim

Fifth of ten. The gap to pdfplumber is 0.016, which is the port working as
intended: pdfgrab reproduces its ancestor's behaviour, including its ceiling.

What pdfgrab wins is **throughput**: 81 ms/doc against pdfplumber's 794 and
camelot lattice's 1554 — roughly **10x faster than anything of comparable
accuracy**, and 4x faster than the system that beat it. Combined with a single
static binary, no Python runtime and no model download, that is a real and
defensible position. "More accurate than Python" is not.

### camelot's `stream` is the result worth studying

It wins outright, and it wins on **recall: 0.762 against pdfgrab's 0.422**.
That is a direct attack on the known weakness — `lines` requires intersecting
rulings, so booktabs-style and horizontally-ruled-only tables are invisible to
it, and 22% of documents yield no table at all.

The instructive part is that this is *not* simply "whitespace inference beats
ruling detection": pdfplumber's equivalent whitespace mode (`text`) scores
**0.248**, the second-worst result in the table. Same idea, very different
implementation. camelot 2.0 rebuilt its backend on `playa-pdf` this year and its
stream flavour is doing something materially better than the algorithm pdfgrab
inherited.

That makes it the highest-value thing to read next, and it is **rule-based** —
so any gain is portable to pure Go with no model, no network and no new
dependency.

## Determinism

The whole table was produced twice, from scratch, in two independent processes,
and the per-document F1 of all ten systems agreed to **every decimal place** —
delta 0.0000 on every row.

That is worth stating rather than assuming. Every system here is rule-based and
runs at a fixed configuration, so there is no sampling to average out, but
"should be deterministic" and "was deterministic" are different claims and only
one of them is a measurement. A number nobody can reproduce is not a result.

## Caveats that must travel with these numbers

**End-to-end, not structure-only.** Published ICDAR 2013 figures of 0.85–0.95
are structure-only: the system is handed the table region. Our own oracle
experiment measures that regime at **0.935**. The two are not comparable and
must never appear in the same column. For reference, the end-to-end
(`GT Border = N`) column of the competition reproduction runs
KYTHE 0.522 · pdf2table 0.585 · TABFIND 0.696 · Nurminen 0.837 ·
FineReader 0.877.

**Detection failure is scored without mercy.** A table missed at IoU < 0.5
contributes *every one of its ground-truth relations* as a false negative, and a
spurious detection contributes every predicted relation as a false positive.
There is no partial credit for a nearly-right region. This is why a 0.935
structure score and a 0.442 end-to-end score are consistent rather than
contradictory.

**Corpus identity.** This is the Smock-corrected **125-PDF superset**
(competition set + the 2012 practice data), not the 67-PDF competition test set
that every historical number above was measured on. The corrected ground truth
is also a slightly more forgiving target — the same TATR checkpoint gains
~1.3 DAR points from the corrections alone.

**Adjacency relations only sees non-blank cells,** and compares them by exact
string match after whitespace normalisation. It is blind to empty-cell
misalignment, and how an implementation decides a cell is "non-empty" moves the
score without any change in structure quality.

**tabula needs `JAVA_HOME`.** Without it, jpype cannot find `libjvm.so`, every
call throws, and the system scores a clean 0.000 that looks like a measurement.
It is not one. The first run of this benchmark hit exactly that and was
discarded.

## Reproduce

```sh
pip install pdfplumber pymupdf camelot-py tabula-py jpype1
export JAVA_HOME=...                      # or tabula silently scores zero
python bench/icdar2013/run.py             # fetches the corpus, builds the extractor
python bench/icdar2013/compare.py \
    ~/.cache/pdfgrab-bench/ICDAR-2013-Table-Competition-Corrected \
    ~/.cache/pdfgrab-bench/bench-extract \
    --gxpdf ~/.cache/pdfgrab-bench/gx-extract
```

Adding a system is one `Adapter` in `systems.py`; a library that is not
installed is reported as skipped rather than scored as zero.
