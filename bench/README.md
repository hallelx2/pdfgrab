# Benchmarks

Accuracy benchmarks against public datasets with published ground truth.

These are **not** `go test` benchmarks (those live next to the code as
`*_bench_test.go` and measure speed). These measure **correctness** against
an external reference, and they are deliberately outside the library's Go
module so their dependencies never reach a consumer of `pdfgrab`.

Datasets are **not committed** — they are large and separately licensed.
Each harness downloads what it needs into a scratch directory.

| Benchmark | Dataset | Measures | Report |
| --- | --- | --- | --- |
| [`icdar2013/`](icdar2013/) | ICDAR 2013 Table Competition (125 PDFs) | table detection + structure | [2026-08-02](../docs/evaluations/2026-08-02-icdar2013-table-structure.md) |
| [`icdar2013/oracle.py`](icdar2013/oracle.py) | same, with ground-truth boundaries | the ceiling a layout model could reach | [2026-08-03](../docs/evaluations/2026-08-03-hybrid-ceiling-oracle-boundaries.md) |

## Why the numbers live in `docs/evaluations/`

A benchmark result is only meaningful with its date, the code version it
ran against, and the caveats on the metric. A bare number in a README rots
the moment either side changes. Every run gets a dated report; the table
above links to the most recent.

## Adding a benchmark

1. `bench/<name>/` with its own `go.mod` if it needs Go.
2. A `run.py` that fetches the dataset and prints a result table.
3. A dated report in `docs/evaluations/`, and a row in the table above.

State the metric precisely, and say what it is **not** comparable to.
Published table-extraction scores in particular are usually
structure-only — the system is handed the table location — and are not
comparable to an end-to-end number.
