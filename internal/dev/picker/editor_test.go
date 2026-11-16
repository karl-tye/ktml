package picker

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/explorer"
)

// TestCommandEditor_GIVENALocation_WHENOpened_THENRunsTheConfiguredCommandForKTMLSourcesInTheProjectOnly
// expands {file}, {line}, and {column}, runs the command itself in the
// project, and refuses files outside the project or not KTML Documents.
func TestCommandEditor_GIVENALocation_WHENOpened_THENRunsTheConfiguredCommandForKTMLSourcesInTheProjectOnly(t *testing.T) {
	var started []*exec.Cmd
	previous := startCommand
	startCommand = func(command *exec.Cmd) error {
		started = append(started, command)
		if command.Args[0] == "missing-editor" {
			return errors.New("not found")
		}
		return nil
	}
	t.Cleanup(func() { startCommand = previous })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "web", "board.ktml")
	for _, file := range []string{source, filepath.Join(root, "main.go"), filepath.Join(filepath.Dir(root), "elsewhere.ktml")} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(filepath.Dir(root), "elsewhere.ktml")) })
	editor := CommandEditor(root, []string{"code", "-g", "{file}:{line}:{column}"})
	if err := editor(explorer.Location{File: "web/board.ktml", Path: source, Line: 12, Column: 3}); err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || !reflect.DeepEqual(started[0].Args, []string{"code", "-g", source + ":12:3"}) || started[0].Dir != root {
		t.Fatalf("started %+v", started)
	}
	for _, location := range []explorer.Location{
		{File: "../elsewhere.ktml", Path: filepath.Join(root, "..", "elsewhere.ktml"), Line: 1, Column: 1},
		{File: "/etc/passwd", Path: "/etc/passwd", Line: 1, Column: 1},
		{File: "main.go", Path: filepath.Join(root, "main.go"), Line: 1, Column: 1},
		{File: "web/board.ktml", Path: source, Line: 0, Column: 1},
		{File: "web/gone.ktml", Path: filepath.Join(root, "web", "gone.ktml"), Line: 1, Column: 1},
	} {
		if err := editor(location); err == nil {
			t.Errorf("opened %+v", location)
		}
	}
	if len(started) != 1 {
		t.Errorf("started %d commands", len(started))
	}
	if err := CommandEditor(root, []string{"missing-editor", "{file}"})(explorer.Location{File: "web/board.ktml", Path: source, Line: 1, Column: 1}); err == nil || !strings.Contains(err.Error(), "missing-editor") {
		t.Errorf("a missing editor: %v", err)
	}
	if err := CommandEditor(root, nil)(explorer.Location{File: "web/board.ktml", Path: source, Line: 1, Column: 1}); err == nil {
		t.Error("an empty command opened")
	}
}
