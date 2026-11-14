package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, src string) string {
	t.Helper()
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestRun_GIVENArguments_WHENRunning_THENExitStatusFollowsTheInventory(t *testing.T) {
	dir := t.TempDir()
	clean := writeFile(t, dir, "clean.templ", "package p\n\ntempl Clean(title string) {\n\t<h1>{ title }</h1>\n}\n")
	blocked := writeFile(t, dir, "blocked.templ", "package p\n\ntempl Blocked(html string) {\n\t@templ.Raw(html)\n}\n")
	report := filepath.Join(dir, "report.json")
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no subcommand", nil, 2},
		{"unknown source language", []string{"jsx", clean, "--report", report, "--dry-run"}, 2},
		{"neither dry run nor output", []string{"templ", clean, "--report", report}, 2},
		{"both dry run and output", []string{"templ", clean, "--report", report, "--dry-run", "--out", dir}, 2},
		{"conversion without types", []string{"templ", clean, "--report", report, "--out", filepath.Join(dir, "out"), "--no-types"}, 2},
		{"missing report", []string{"templ", clean, "--dry-run"}, 2},
		{"clean file", []string{"templ", clean, "--report", report, "--dry-run", "--no-types", "--root", dir}, 0},
		{"blocked node", []string{"templ", "--report", report, blocked, "--dry-run", "--no-types", "--root", dir}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != tt.want {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", got, tt.want, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRun_GIVENDryRun_WHENRunning_THENTheJSONReportAndSummaryAreWritten(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "a.templ", "package p\n\ntempl A(html string) {\n\t<div>\n\t\t@templ.Raw(html)\n\t</div>\n}\n")
	report := filepath.Join(dir, "report.json")
	summary := filepath.Join(dir, "summary.md")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"templ", dir + "/...", "--report", report, "--summary", summary, "--dry-run", "--no-types", "--root", dir}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d, want 1 (templ.Raw is blocked)\n%s", got, stderr.String())
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Schema string `json:"schema"`
		Files  []struct {
			Path string `json:"path"`
		} `json:"files"`
		Gaps []struct {
			Name string `json:"name"`
		} `json:"gaps"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema == "" || len(decoded.Files) != 1 || decoded.Files[0].Path != filepath.Base(file) {
		t.Errorf("report = %+v", decoded)
	}
	if len(decoded.Gaps) != 1 || decoded.Gaps[0].Name != "trusted-html" {
		t.Errorf("gaps = %+v", decoded.Gaps)
	}
	text, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "`trusted-html`") {
		t.Errorf("summary lacks the blocker:\n%s", text)
	}
	if !strings.Contains(stdout.String(), "1 blocked") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestRun_GIVENNodesUnconverted_WHENRunning_THENOnlyUnconvertedNodesAreListed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.templ", "package p\n\ntempl A(title string) {\n\t<h1>{ title }</h1>\n\t<p>{ fmt.Sprint(title) }</p>\n}\n")
	report := filepath.Join(dir, "report.json")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"templ", dir, "--report", report, "--dry-run", "--no-types", "--root", dir, "--nodes", "unconverted"}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d\n%s", got, stderr.String())
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Totals struct {
			Converted int `json:"converted"`
		} `json:"totals"`
		NodesOmitted string `json:"nodesOmitted"`
		Nodes        []struct {
			Status string `json:"status"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.NodesOmitted != "converted" || decoded.Totals.Converted == 0 || len(decoded.Nodes) != 1 || decoded.Nodes[0].Status != "presentation_scaffolded" {
		t.Errorf("report = %+v", decoded)
	}
}

// copyFixture copies the typed fixture module, whose go.mod lets the
// converter load types and resolve the output package.
func copyFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "templmigrate", "testdata", "typed")
	dir := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRun_GIVENOut_WHENConverting_THENWritesKTMLAndExitsOnPartialNodes(t *testing.T) {
	dir := copyFixture(t)
	tests := []struct {
		name string
		path string
		want int
	}{
		{"every node converts", filepath.Join(dir, "convert") + "/...", 0},
		{"templ.Raw, a once handle, and an unexported helper are partial", filepath.Join(dir, "fixture"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(dir, "out-"+strings.ReplaceAll(tt.name, " ", "-"))
			report := filepath.Join(dir, "report.json")
			var stdout, stderr bytes.Buffer
			if got := run([]string{"templ", tt.path, "--root", dir, "--out", out, "--scaffold-presenters", "--report", report}, &stdout, &stderr); got != tt.want {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", got, tt.want, stdout.String(), stderr.String())
			}
			matches, _ := filepath.Glob(filepath.Join(out, "*", "*.ktml"))
			if len(matches) == 0 {
				t.Errorf("no .ktml written under %s", out)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Schema     string `json:"schema"`
				Conversion struct {
					Outputs []struct {
						Kind string `json:"kind"`
					} `json:"outputs"`
				} `json:"conversion"`
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Schema != "ktml.migrate.templ.conversion/v1" || len(decoded.Conversion.Outputs) == 0 {
				t.Errorf("report = %+v", decoded)
			}
			if !strings.Contains(stdout.String(), "presenter fields") {
				t.Errorf("stdout = %q", stdout.String())
			}
		})
	}
}
