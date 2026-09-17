// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// fixturePath assembles the path to a fixture relative to this test
// file. We use a relative path because `go test ./...` runs tests
// with the test file's directory as the cwd.
func fixturePath(name string) string {
	return filepath.Join("..", "..", "testdata", "golden", name)
}

// TestRun_ExtractTables_JSON exercises the happy path: extract tables
// from the issue-466-example fixture in JSON format and assert the
// shape matches the documented schema.
func TestRun_ExtractTables_JSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"extract", fixturePath("issue-466-example.pdf"), "--tables", "--format", "json"}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (stderr=%s)", err, stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	pages, ok := out["pages"].([]any)
	if !ok {
		t.Fatalf("output missing pages key: %v", out)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	page := pages[0].(map[string]any)
	tables, ok := page["tables"].([]any)
	if !ok || len(tables) < 1 {
		t.Fatalf("page has no tables: %v", page)
	}
	first := tables[0].(map[string]any)
	if first["bbox"] == nil {
		t.Errorf("table 0 missing bbox")
	}
	rows, ok := first["rows"].([]any)
	if !ok || len(rows) == 0 {
		t.Errorf("table 0 missing rows")
	}
}

// TestRun_ExtractTables_TextStrategy asserts the CLI propagates the
// --vertical-strategy / --horizontal-strategy flags through to the
// library and recovers the borderless fixture's table.
func TestRun_ExtractTables_TextStrategy(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{
		"extract",
		fixturePath("table-3x4-borderless.pdf"),
		"--tables",
		"--vertical-strategy", "text",
		"--horizontal-strategy", "text",
		"--format", "text",
	}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Item", "Quantity", "Price", "Apple", "Banana", "Cherry"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// TestRun_ExtractTables_Pages asserts the --pages flag narrows the
// output to the requested pages only.
func TestRun_ExtractTables_Pages(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{
		"extract",
		fixturePath("issue-466-example.pdf"),
		"--tables",
		"--format", "json",
		"--pages", "1",
	}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (stderr=%s)", err, stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pages := out["pages"].([]any); len(pages) != 1 {
		t.Errorf("got %d pages, want 1", len(pages))
	}
}

// TestRun_ExtractText asserts the --text mode emits JSON with a
// "text" field per page.
func TestRun_ExtractText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"extract", fixturePath("hello.pdf"), "--text", "--format", "json"}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Hello") {
		t.Errorf("output missing 'Hello':\n%s", stdout.String())
	}
}

// TestParsePages exercises the page-spec parser on a variety of valid
// and invalid inputs.
func TestParsePages(t *testing.T) {
	cases := []struct {
		name  string
		spec  string
		total int
		want  []int
		err   bool
	}{
		{"empty_all", "", 3, []int{1, 2, 3}, false},
		{"single", "2", 3, []int{2}, false},
		{"comma_list", "1,3", 3, []int{1, 3}, false},
		{"dash_range", "2-4", 5, []int{2, 3, 4}, false},
		{"mixed", "1,3-5", 5, []int{1, 3, 4, 5}, false},
		{"deduped", "1,1,2", 3, []int{1, 2}, false},
		{"bad_range", "5-2", 5, nil, true},
		{"out_of_bounds", "10", 3, nil, true},
		{"negative", "-1", 3, nil, true},
		{"invalid", "abc", 3, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parsePages(c.spec, c.total)
			if c.err {
				if err == nil {
					t.Errorf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("[%d] got %d, want %d", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestReorderFlagsLast asserts that positional arguments get pushed
// to the end so the standard-library flag package can parse them
// regardless of their original position.
func TestReorderFlagsLast(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{
			in:   []string{"file.pdf", "--tables", "--format", "json"},
			want: []string{"--tables", "--format", "json", "file.pdf"},
		},
		{
			in:   []string{"--pages", "1-3", "file.pdf"},
			want: []string{"--pages", "1-3", "file.pdf"},
		},
		{
			in:   []string{"--tables", "file.pdf"},
			want: []string{"--tables", "file.pdf"},
		},
		{
			in:   []string{"--format=json", "file.pdf"},
			want: []string{"--format=json", "file.pdf"},
		},
	}
	for i, c := range cases {
		got := reorderFlagsLast(c.in)
		if len(got) != len(c.want) {
			t.Errorf("case %d: lengths differ; got %v, want %v", i, got, c.want)
			continue
		}
		for j := range got {
			if got[j] != c.want[j] {
				t.Errorf("case %d [%d]: got %q, want %q", i, j, got[j], c.want[j])
			}
		}
	}
}

// TestRun_MissingFile asserts the CLI surfaces a clean error when the
// input path doesn't exist (rather than crashing).
func TestRun_MissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"extract", "no-such-file.pdf", "--tables"}
	err := run(args, &stdout, &stderr)
	if err == nil {
		t.Fatal("got nil error, want failure")
	}
}

// TestRun_MutuallyExclusiveFlags asserts --tables and --text can't
// both be set.
func TestRun_MutuallyExclusiveFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"extract", fixturePath("hello.pdf"), "--tables", "--text"}
	err := run(args, &stdout, &stderr)
	if err == nil {
		t.Fatal("got nil error, want mutually-exclusive failure")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q should mention mutual exclusion", err)
	}
}

// TestRun_UnknownSubcommand asserts the CLI rejects an unknown
// subcommand with a clear error.
func TestRun_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"foo"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("got nil, want unknown-subcommand error")
	}
}

// TestRun_Version asserts the `version` subcommand names the tool and
// reports whatever version was injected at build time.
//
// It deliberately does NOT assert a version literal. The previous form
// pinned "v0.3.0", so the CLI kept reporting v0.3.0 through the entire
// v0.4.0 release and the test agreed with it. A test that has to be
// hand-edited every release will drift the moment someone forgets.
func TestRun_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run version: %v", err)
	}
	got := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(got, "pdfgrab ") {
		t.Errorf("version output does not name the tool: %q", got)
	}
	if strings.TrimPrefix(got, "pdfgrab ") == "" {
		t.Errorf("version output carries no version: %q", got)
	}
}
