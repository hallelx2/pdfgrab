# Third-party notices

pdfgrab itself is MIT-licensed (see `LICENSE`). It bundles no third-party
*code*, but several generated data tables in `internal/pdf/` are derived from
third-party sources. Those sources and their license terms are reproduced
below.

| File | Derived from | License |
| --- | --- | --- |
| `internal/pdf/afm_widths.go` | Adobe Core 14 AFM metrics, via ReportLab's `_fontdata_widths_*.py` | BSD-3-Clause |
| `internal/pdf/symbol_glyphs.go` | Adobe Glyph List (`glyphlist.txt`) | BSD-3-Clause |
| `internal/pdf/zapfdingbats_glyphs.go` | Adobe Glyph List (`zapfdingbats.txt`) | BSD-3-Clause |
| `internal/pdf/standard14_encodings.go` | Adobe Glyph List, plus the encoding vectors in pdf.js `src/core/encodings.js` | BSD-3-Clause and Apache-2.0 |

---

## Adobe Glyph List (agl-aglfn)

<https://github.com/adobe-type-tools/agl-aglfn>

Source of `glyphlist.txt` and `zapfdingbats.txt`, from which the glyph-name to
Unicode tables and the Symbol / ZapfDingbats built-in encodings were generated.
The Core 14 AFM advance widths originate with Adobe as well.

```
Copyright 2002-2019 Adobe (http://www.adobe.com/).

Redistribution and use in source and binary forms, with or
without modification, are permitted provided that the
following conditions are met:

Redistributions of source code must retain the above
copyright notice, this list of conditions and the following
disclaimer.

Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following
disclaimer in the documentation and/or other materials
provided with the distribution.

Neither the name of Adobe nor the names of its contributors
may be used to endorse or promote products derived from this
software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND
CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES,
INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR
CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT
NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR
OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

---

## ReportLab

<https://github.com/MrBitBucket/reportlab-mirror>

The Core 14 advance widths in `afm_widths.go` were taken from ReportLab's
`reportlab/pdfbase/_fontdata_widths_*.py`, which mirror Adobe's public Core 14
AFM files. ReportLab is distributed under the BSD-3-Clause license,
Copyright (c) 2000-2024, ReportLab Europe Ltd. The full license text ships with
ReportLab as `LICENSE.txt`.

---

## pdf.js

<https://github.com/mozilla/pdf.js>

The Symbol and ZapfDingbats built-in encoding vectors in
`standard14_encodings.go` were generated from the `SymbolSetEncoding` and
`ZapfDingbatsEncoding` arrays in `src/core/encodings.js`.

```
Copyright 2012 Mozilla Foundation

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
