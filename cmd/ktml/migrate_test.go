package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateCmd_GIVENTheTool_WHENRun_THENArgumentsAndExitStatusPassThrough(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"args: $*\"\necho problem >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, migrateTool), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var stdout, stderr bytes.Buffer
	got := migrateCmd(strings.NewReader(""), &stdout, &stderr, []string{"templ", "./internal/...", "--report", "r.json", "--dry-run"})
	if got != 3 {
		t.Errorf("exit = %d, want 3", got)
	}
	if stdout.String() != "args: templ ./internal/... --report r.json --dry-run\n" || stderr.String() != "problem\n" {
		t.Errorf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestMigrateCmd_GIVENNoTool_WHENRun_THENExplainsHowToInstallIt(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var stdout, stderr bytes.Buffer
	if got := migrateCmd(strings.NewReader(""), &stdout, &stderr, []string{"templ"}); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), migrateInstall) {
		t.Errorf("stderr = %q", stderr.String())
	}
}
