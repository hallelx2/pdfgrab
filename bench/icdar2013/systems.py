"""Table-extraction systems under comparison, as pluggable adapters.

Every adapter answers the same question — "what tables are on this page,
as grids of cell text" — and the scorer turns those grids into adjacency
relations. That keeps the comparison honest: each library is scored on
the structure it reports, not on how it chose to serialise it.

An adapter that cannot import its library is *unavailable*, not broken.
The suite reports which systems ran and which were skipped, so a partial
environment produces a partial table rather than a crash or, worse, a
silently missing row that reads as a zero.

Contract for an extract function:

    extract(pdf_path: str) -> list[list[list[str]]]

i.e. a list of tables, each a list of rows, each a list of cell strings.
Cells may be None or empty; the scorer normalises whitespace.

Adding a system: write the function, add one Adapter to ADAPTERS. Do not
reach into the scorer.
"""

from __future__ import annotations

import importlib
import os
import subprocess
import time
from dataclasses import dataclass, field
from typing import Callable

Grid = list[list[str]]
Tables = list[Grid]


@dataclass
class Adapter:
    """One system under test."""

    name: str
    extract: Callable[[str], Tables]

    # Import name checked for availability, plus the pip target that
    # provides it, so a skip message can tell the reader how to fix it.
    module: str | None = None
    install: str = ""

    # Free-text note carried into the report — a caveat that would
    # otherwise be lost between running the benchmark and reading it.
    note: str = ""

    # Was this system trained on data drawn from the same distribution as
    # the test corpus? Every deep-learning table model is trained on
    # PubTables-1M / FinTabNet / PubTabNet, which overlap this benchmark's
    # document population; rule-based extractors are trained on nothing.
    # Comparing the two without saying so flatters the learned systems.
    trained_on_distribution: bool = False

    # Was the table region handed to the system? Everything here is
    # end-to-end (it must find the table itself), but the flag exists so an
    # oracle-boundary row can sit in the same table without being mistaken
    # for a comparable one. A missed table donates ALL of its relations to
    # false negatives, so this single bit moves F1 by 2-3x.
    region_given: bool = False

    def available(self) -> tuple[bool, str]:
        if self.module is None:
            return True, ""
        try:
            importlib.import_module(self.module)
            return True, ""
        except Exception as e:
            return False, f"{type(e).__name__}: {e}"

    def version(self) -> str:
        if self.module is None:
            return ""
        try:
            m = importlib.import_module(self.module)
            return str(getattr(m, "__version__", "") or getattr(m, "VERSION", "") or "")
        except Exception:
            return ""


@dataclass
class Timing:
    """Wall-clock per system, accumulated across documents.

    Worth measuring alongside accuracy. A library that is 3% more accurate
    and 40x slower is not obviously the better choice for a pipeline that
    ingests documents on a request path, and a benchmark that reports only
    F1 hides that trade entirely.
    """

    seconds: float = 0.0
    docs: int = 0
    failures: int = 0
    per_doc: list[float] = field(default_factory=list)

    def add(self, dt: float) -> None:
        self.seconds += dt
        self.docs += 1
        self.per_doc.append(dt)

    def mean_ms(self) -> float:
        return 1000.0 * self.seconds / self.docs if self.docs else 0.0

    def p95_ms(self) -> float:
        if not self.per_doc:
            return 0.0
        ordered = sorted(self.per_doc)
        idx = min(len(ordered) - 1, int(0.95 * len(ordered)))
        return 1000.0 * ordered[idx]


def timed(fn: Callable[[str], Tables], pdf: str, t: Timing) -> Tables:
    """Run fn, recording wall-clock and swallowing per-document failures.

    A library that throws on one malformed PDF should score zero for that
    document, not abort the whole run — but the failure is counted and
    reported, because "extracted nothing" and "crashed" are different
    facts about a library.
    """
    start = time.perf_counter()
    try:
        out = fn(pdf)
    except Exception:
        t.failures += 1
        out = []
    t.add(time.perf_counter() - start)
    return out


# --- adapters ---------------------------------------------------------


def pdfgrab(exe: str, strategy: str, merge: bool = False) -> Callable[[str], Tables]:
    """pdfgrab, via the benchmark's Go extractor binary."""

    def run(pdf: str) -> Tables:
        import json

        cmd = [exe, "-strategy", strategy]
        if merge:
            cmd.append("-merge")
        cmd.append(pdf)
        out = subprocess.run(cmd, capture_output=True, timeout=120).stdout
        return [t["rows"] for t in json.loads(out or b"[]")]

    return run


def gxpdf_tables(exe: str) -> Callable[[str], Tables]:
    """coregx/gxpdf, via a sibling Go extractor binary.

    The one direct competitor pdfgrab has inside Go: MIT, pure Go (no CGo),
    and the only permissively-licensed Go library that claims table
    extraction. Its own docs claim "100% accuracy on bank statements",
    which is a narrow enough claim to be worth testing on a general corpus.

    Built separately rather than linked, so a panic or a hang in a
    third-party library cannot take the harness down with it.
    """

    def run(pdf: str) -> Tables:
        import json

        out = subprocess.run([exe, pdf], capture_output=True, timeout=120).stdout
        return [t["rows"] for t in json.loads(out or b"[]")]

    return run


def pdfplumber_tables(strategy: str) -> Callable[[str], Tables]:
    def run(pdf: str) -> Tables:
        import pdfplumber

        settings = {"vertical_strategy": strategy, "horizontal_strategy": strategy}
        tables: Tables = []
        with pdfplumber.open(pdf) as doc:
            for page in doc.pages:
                tables.extend(page.extract_tables(settings))
        return tables

    return run


def pymupdf_tables(pdf: str) -> Tables:
    """PyMuPDF's find_tables, added in 1.23.

    Its own strategy selection is internal, so there is no per-axis knob
    to match against the others — it is scored as the library ships.
    """
    try:
        import pymupdf
    except ImportError:  # <1.24 shipped only the legacy name
        import fitz as pymupdf

    tables: Tables = []
    with pymupdf.open(pdf) as doc:
        for page in doc:
            for tbl in page.find_tables().tables:
                tables.append(tbl.extract())
    return tables


def camelot_tables(flavor: str) -> Callable[[str], Tables]:
    """Camelot. 'lattice' needs ruled cells; 'stream' infers from whitespace.

    Camelot reads a page range rather than a document, so 'all' is passed
    explicitly — its default is page 1 only, which would quietly score a
    multi-page document on its first page and look like a recall problem.
    """

    def run(pdf: str) -> Tables:
        import camelot

        tables: Tables = []
        for t in camelot.read_pdf(pdf, pages="all", flavor=flavor, suppress_stdout=True):
            tables.append([[str(c) for c in row] for row in t.df.values.tolist()])
        return tables

    return run


def tabula_tables(lattice: bool) -> Callable[[str], Tables]:
    """tabula-py, the Java tabula wrapper. Needs a JVM on PATH."""

    def run(pdf: str) -> Tables:
        import tabula

        dfs = tabula.read_pdf(
            pdf, pages="all", lattice=lattice, stream=not lattice,
            multiple_tables=True, silent=True,
        )
        tables: Tables = []
        for df in dfs:
            # tabula promotes the first row to a header; put it back, or
            # every table silently loses its header row and with it the
            # vertical relations that row participates in.
            header = [str(c) for c in df.columns.tolist()]
            rows = [[str(c) for c in row] for row in df.values.tolist()]
            if any(not h.startswith("Unnamed") for h in header):
                rows.insert(0, header)
            tables.append(rows)
        return tables

    return run


def build_adapters(exe: str, gx_exe: str = "") -> list[Adapter]:
    """The comparison set.

    `exe` is the built pdfgrab extractor; `gx_exe` the gxpdf one, which is
    optional because it is a third-party Go module the harness should not
    require.
    """
    adapters = [
        Adapter("pdfgrab (lines)", pdfgrab(exe, "lines"),
                note="the library under test"),
        Adapter("pdfgrab (auto)", pdfgrab(exe, "auto"),
                note="one-axis-ruled support, opt-in"),
        Adapter("pdfplumber (lines)", pdfplumber_tables("lines"),
                module="pdfplumber", install="pdfplumber",
                note="the implementation pdfgrab is a port of"),
        Adapter("pdfplumber (text)", pdfplumber_tables("text"),
                module="pdfplumber", install="pdfplumber",
                note="whitespace-inferred; high recall, low precision"),
        Adapter("PyMuPDF find_tables", pymupdf_tables,
                module="pymupdf", install="pymupdf"),
        # camelot 2.0 dropped Ghostscript for pdfium and the [base] extra
        # no longer exists — pip warns and installs the bare package.
        Adapter("camelot (lattice)", camelot_tables("lattice"),
                module="camelot", install="camelot-py",
                note="ruled cells; pdfium backend since 1.0"),
        Adapter("camelot (stream)", camelot_tables("stream"),
                module="camelot", install="camelot-py",
                note="whitespace-inferred"),
        Adapter("tabula (lattice)", tabula_tables(True),
                module="tabula", install="tabula-py",
                note="DORMANT: last release 2024-10; JVM required"),
        Adapter("tabula (stream)", tabula_tables(False),
                module="tabula", install="tabula-py",
                note="DORMANT: last release 2024-10; JVM required"),
    ]

    if gx_exe and os.path.exists(gx_exe):
        # Inserted right after pdfgrab: Go-vs-Go is the comparison that
        # decides whether pdfgrab is worth maintaining at all, so it should
        # sit next to it in the output rather than at the bottom.
        adapters.insert(2, Adapter(
            "gxpdf (Go)", gxpdf_tables(gx_exe),
            note="the only other permissively-licensed Go table extractor"))

    return adapters
