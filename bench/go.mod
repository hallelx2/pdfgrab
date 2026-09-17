// Separate module on purpose: the benchmark harness must never add a
// dependency to the library it measures. Nested modules are excluded from
// the parent's ./... , so go build ./... at the repo root ignores this
// entirely and a consumer of pdfgrab never fetches it.
module github.com/hallelx2/pdfgrab/bench

go 1.25.0

require github.com/hallelx2/pdfgrab v0.0.0

require (
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.2 // indirect
	github.com/hhrutter/tiff v1.0.3 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/pdfcpu/pdfcpu v0.12.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/image v0.39.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

replace github.com/hallelx2/pdfgrab => ../
