package picker

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/explorer"
)

// startCommand starts an editor command; tests replace it.
var startCommand = func(command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	go func() {
		if err := command.Wait(); err != nil {
			slog.Warn("The picker's editor command failed", "command", command.Args[0], "error", err)
		}
	}()
	return nil
}

// CommandEditor opens sources with args, dev.picker.editor, run directly
// (never by a shell) in root. {file}, {line}, and {column} in each argument
// are replaced by the source's absolute path, line, and column. It opens only
// existing .ktml Documents inside root.
func CommandEditor(root string, args []string) Editor {
	return func(location explorer.Location) error {
		if len(args) == 0 {
			return errors.New("no editor is configured: set dev.picker.editor")
		}
		// Both sides are compared without symbolic links; the file must
		// exist.
		file, err := filepath.EvalSymlinks(location.Path)
		if err == nil {
			file, err = filepath.Abs(file)
		}
		if err != nil {
			return fmt.Errorf("the picker cannot open %s: %w", location.File, err)
		}
		project := root
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			project = resolved
		}
		relative, err := filepath.Rel(project, file)
		if err != nil || !filepath.IsLocal(relative) || filepath.Ext(file) != ".ktml" || location.Line < 1 || location.Column < 1 {
			return fmt.Errorf("the picker only opens KTML Documents inside the project, not %s", location.File)
		}
		replacer := strings.NewReplacer("{file}", file, "{line}", strconv.Itoa(location.Line), "{column}", strconv.Itoa(location.Column))
		expanded := make([]string, len(args))
		for index, arg := range args {
			expanded[index] = replacer.Replace(arg)
		}
		command := exec.Command(expanded[0], expanded[1:]...)
		command.Dir = root
		if err := startCommand(command); err != nil {
			return fmt.Errorf("start the editor %s: %w", args[0], err)
		}
		return nil
	}
}
