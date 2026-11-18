package picker

import (
	"log/slog"
	"os/exec"
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
	return (&Opener{Source: "dev.picker.editor", words: args, style: styleCommand}).Editor(root)
}
