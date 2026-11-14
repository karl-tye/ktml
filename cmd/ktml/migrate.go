package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// migrateTool is the migration command. It lives in the separate
// github.com/karl-tye/ktml/migrate module so that templ, which it parses,
// stays out of KTML's own dependency graph.
const migrateTool = "ktml-migrate"

const migrateInstall = "go install github.com/karl-tye/ktml/migrate/cmd/ktml-migrate"

// migrateCmd runs ktml migrate by executing ktml-migrate with the same
// arguments. It looks next to the running ktml executable first, then on
// PATH, and returns the tool's exit status.
func migrateCmd(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	tool, err := findMigrateTool()
	if err != nil {
		fmt.Fprintf(stderr, "ktml migrate: %v; install it with: %s\n", err, migrateInstall)
		return 2
	}
	cmd := exec.Command(tool, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(stderr, "ktml migrate: %v\n", err)
		return 2
	}
	return 0
}

func findMigrateTool() (string, error) {
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), migrateTool)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	tool, err := exec.LookPath(migrateTool)
	if err != nil {
		return "", fmt.Errorf("%s not found next to ktml or on PATH", migrateTool)
	}
	return tool, nil
}
