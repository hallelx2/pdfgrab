#!/usr/bin/env bash
# Regenerates testdata/encrypted/ from testdata/table-2x3-ruled.pdf with qpdf.
# The passwords are fixture values, not secrets: the point of the second file
# is that pdfgrab cannot open it without one.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p testdata/encrypted
qpdf --encrypt "" fixture-owner-pw 256 -- testdata/table-2x3-ruled.pdf testdata/encrypted/owner-password-only.pdf
qpdf --encrypt fixture-user-pw fixture-owner-pw 256 -- testdata/table-2x3-ruled.pdf testdata/encrypted/user-password.pdf
