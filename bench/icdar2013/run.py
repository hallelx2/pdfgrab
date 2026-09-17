"""Run the ICDAR 2013 table benchmark end to end.

    pip install pdfplumber
    python bench/icdar2013/run.py

Downloads the dataset (~12 MB) into a scratch directory, builds the Go
extractor, scores pdfgrab against the ground truth, and prints a result
table. pdfplumber is scored alongside as a reference point — the question
"is 0.36 good?" is unanswerable without a baseline, and pdfplumber is the
implementation pdfgrab is a port of.

Pass --limit N to score only the first N documents while iterating.
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tarfile
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))

# Brandon Smock's corrected edition of the ICDAR 2013 competition set.
# The original release had known ground-truth errors; this is the version
# current table-structure papers evaluate against.
URL = (
    "https://huggingface.co/datasets/bsmock/"
    "ICDAR-2013-Table-Competition-Corrected/resolve/main/"
    "ICDAR-2013-Table-Competition-Corrected.tar.gz"
)
DIRNAME = "ICDAR-2013-Table-Competition-Corrected"


def scratch() -> str:
    d = os.environ.get("PDFTABLE_BENCH_DIR") or os.path.join(
        os.path.expanduser("~"), ".cache", "pdfgrab-bench"
    )
    os.makedirs(d, exist_ok=True)
    return d


def fetch(dest: str) -> str:
    root = os.path.join(dest, DIRNAME)
    if os.path.isdir(root):
        print(f"dataset already present: {root}")
        return root
    tgz = os.path.join(dest, "icdar2013.tar.gz")
    if not os.path.exists(tgz):
        print(f"downloading {URL}")
        urllib.request.urlretrieve(URL, tgz)
    print("extracting...")
    with tarfile.open(tgz) as t:
        t.extractall(dest)
    return root


def build_extractor(dest: str) -> str:
    exe = os.path.join(dest, "bench-extract.exe" if os.name == "nt" else "bench-extract")
    mod = os.path.join(dest, "extractor")
    os.makedirs(mod, exist_ok=True)
    shutil.copy(os.path.join(HERE, "extract.go"), os.path.join(mod, "main.go"))

    # Its own module, pointed at the working tree: the benchmark must never
    # add a dependency to the library it is measuring.
    gomod = os.path.join(mod, "go.mod")
    if not os.path.exists(gomod):
        subprocess.run(["go", "mod", "init", "benchextract"], cwd=mod, check=True,
                       capture_output=True)
    subprocess.run(
        ["go", "mod", "edit", "-replace",
         f"github.com/hallelx2/pdfgrab={REPO.replace(os.sep, '/')}"],
        cwd=mod, check=True, capture_output=True)
    subprocess.run(
        ["go", "mod", "edit", "-require", "github.com/hallelx2/pdfgrab@v0.0.0"],
        cwd=mod, check=True, capture_output=True)
    subprocess.run(["go", "mod", "tidy"], cwd=mod, check=True, capture_output=True)
    subprocess.run(["go", "build", "-o", exe, "."], cwd=mod, check=True)
    print(f"built {exe}")
    return exe


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--limit", type=int, default=0,
                    help="score only the first N documents")
    ap.add_argument("--diag", action="store_true",
                    help="also run the detection-vs-structure diagnostic")
    args = ap.parse_args()

    dest = scratch()
    root = fetch(dest)
    exe = build_extractor(dest)

    cmd = [sys.executable, os.path.join(HERE, "score.py"), root, exe]
    if args.limit:
        cmd.append(str(args.limit))
    subprocess.run(cmd, check=True)

    if args.diag:
        print()
        subprocess.run(
            [sys.executable, os.path.join(HERE, "diag.py"), root, exe], check=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
