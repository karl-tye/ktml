package picker

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// findTerminal returns the command that runs a terminal editor in a new
// terminal window, to which the editor's command is appended:
// xdg-terminal-exec when it is installed, else $TERMINAL with the flag that
// introduces its command. macOS's Terminal cannot be given a command
// without a shell script, so a Mac needs $TERMINAL or dev.picker.editor.
func findTerminal(getenv func(string) string) ([]string, error) {
	if path, err := exec.LookPath("xdg-terminal-exec"); err == nil {
		return []string{path}, nil
	}
	value := strings.TrimSpace(getenv("TERMINAL"))
	if value == "" {
		return nil, errors.New("xdg-terminal-exec is not installed and $TERMINAL is not set")
	}
	words, err := splitWords(value)
	if err != nil {
		return nil, fmt.Errorf("$TERMINAL (%q) is not a command ktml dev can run without a shell: %v", value, err)
	}
	path, err := exec.LookPath(words[0])
	if err != nil {
		return nil, fmt.Errorf("$TERMINAL names %s, which is not installed", words[0])
	}
	words[0] = path
	return append(words, terminalFlag(filepath.Base(path))...), nil
}

// terminalFlag is what goes between a terminal's command and the command it
// runs.
func terminalFlag(name string) []string {
	switch name {
	case "xdg-terminal-exec", "foot", "kitty":
		return nil
	case "gnome-terminal", "kgx", "ptyxis":
		return []string{"--"}
	case "wezterm":
		return []string{"start", "--"}
	case "xfce4-terminal", "mate-terminal":
		return []string{"-x"}
	}
	return []string{"-e"}
}
