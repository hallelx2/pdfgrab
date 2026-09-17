# ICDAR 2013 Table Competition benchmark

Measures **table detection + structure recognition** end to end against
125 born-digital PDFs from EU and US government sources, with per-cell
ground truth.

```sh
pip install pdfplumber
python bench/icdar2013/run.py            # full run, ~10 min
python bench/icdar2013/run.py --limit 5  # quick check while iterating
python bench/icdar2013/run.py --diag     # detection vs structure breakdown
```

The dataset (~12 MB) downloads to `~/.cache/pdfgrab-bench` (override with
`PDFTABLE_BENCH_DIR`). It is not committed.

## Metric: adjacency relations

From Göbel et al., the metric the competition itself used. For every
non-empty cell, take its nearest non-empty neighbour to the right and
below; each pair is a relation `(text_a, text_b, direction)`. Score the
detected multiset against ground truth.

Cell-by-cell grid comparison would be the obvious alternative and is
worse: two tools can grid the same table differently — one emits a spacer
column, the other does not — and still convey identical structure. A grid
diff calls that a failure. Adjacency asks the question a reader actually
cares about: *is this value next to that label?* It is unforgiving about
genuinely wrong structure and forgiving about harmless disagreements.

Documents are scored whole, with relations unioned across pages and
tables. That sidesteps matching detected tables to ground-truth tables, a
step which would need its own arbitrary thresholds and would make the
number depend on them.

## Reading the result

**This is an end-to-end number and is NOT comparable to published ICDAR
2013 scores of 0.85–0.95.** Those evaluate structure recognition with the
table region *given*; this has to find the table first. Compare runs of
this harness to each other, and to the pdfplumber column, not to papers.

`--diag` splits the two failure modes, which need opposite work:

- **the table was never detected** — a detection problem
- **it was detected but gridded wrongly** — a structure problem

Latest results: [`docs/evaluations/`](../../docs/evaluations/).

## Files

| | |
| --- | --- |
| `run.py` | fetches the dataset, builds the extractor, runs everything |
| `extract.go` | dumps every table pdfgrab finds as JSON; built as its own module so the benchmark adds no dependency to the library |
| `score.py` | parses ground-truth XML, computes precision/recall/F1 |
| `diag.py` | separates detection failures from structure failures |
