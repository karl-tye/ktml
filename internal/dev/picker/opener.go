package picker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/karl-tye/ktml/internal/explorer"
)

// Opener is how Ctrl+click opens a picked element's source: the command of
// dev.picker.editor, or the editor that ktml dev's own $VISUAL or $EDITOR
// names.
type Opener struct {
	// Source is where the opener came from: "dev.picker.editor", "$VISUAL",
	// or "$EDITOR".
	Source string
	// Name is the editor's program name, such as "nvim", for the picker's
	// hint.
	Name string
	// words is the configured command, or the variable's words.
	words []string
	style style
	// syntax is how the editor is told the file and position.
	syntax syntax
	getenv func(string) string
}

// style is the way an opener runs its editor.
type style int

const (
	// styleCommand runs dev.picker.editor, expanding {file}, {line}, and
	// {column}.
	styleCommand style = iota
	// styleGUI runs a graphical editor, which opens its own window.
	styleGUI
	// styleNeovim opens the file in a running Neovim that the KTML plugin
	// registered, or else runs nvim in a new terminal.
	styleNeovim
	// styleTerminal runs a terminal editor in a new terminal window.
	styleTerminal
)

// syntax is how an editor's command line names a file and a position.
type syntax int

const (
	syntaxFile        syntax = iota // file
	syntaxGotoFlag                  // -g file:line:column (VS Code and its forks)
	syntaxColon                     // file:line:column
	syntaxJetBrains                 // --line line --column column file
	syntaxEmacsclient               // -n +line:column file
	syntaxPlusColon                 // +line:column file
	syntaxPlusComma                 // +line,column file
	syntaxPlusLine                  // +line file
	syntaxVimCursor                 // "+call cursor(line,column)" file
)

// editorKind is what ktml dev knows about an editor program.
type editorKind struct {
	style  style
	syntax syntax
	// blocking are flags that make the editor wait until the file is
	// closed; they are dropped so that the request returns.
	blocking []string
	// terminalFlags are flags that make a graphical editor run in the
	// terminal instead.
	terminalFlags []string
}

var (
	waitFlags      = []string{"-w", "--wait"}
	jetBrainsNames = []string{"idea", "idea64", "goland", "webstorm", "pycharm", "pycharm-community", "pycharm-professional", "phpstorm", "clion", "rider", "rubymine", "datagrip", "dataspell", "rustrover", "aqua", "studio"}
)

// kindOf returns what ktml dev knows about an editor program by its base
// name. Unknown editors run in a terminal with the file alone: every editor
// takes a file name, while +line would be read as a second file by some.
func kindOf(name string) editorKind {
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".sh")
	switch {
	case slices.Contains([]string{"code", "code-insiders", "codium", "vscodium", "cursor", "windsurf"}, name):
		return editorKind{style: styleGUI, syntax: syntaxGotoFlag, blocking: waitFlags}
	case slices.Contains([]string{"zed", "zeditor", "subl", "sublime_text"}, name):
		return editorKind{style: styleGUI, syntax: syntaxColon, blocking: waitFlags}
	case slices.Contains(jetBrainsNames, name):
		return editorKind{style: styleGUI, syntax: syntaxJetBrains, blocking: []string{"--wait"}}
	case name == "emacsclient":
		return editorKind{style: styleGUI, syntax: syntaxEmacsclient, blocking: []string{"-n", "--no-wait"}, terminalFlags: []string{"-t", "-nw", "--tty"}}
	case name == "emacs":
		return editorKind{style: styleGUI, syntax: syntaxPlusColon, terminalFlags: []string{"-nw", "--no-window-system", "-t", "--terminal"}}
	case name == "gvim" || name == "mvim":
		return editorKind{style: styleGUI, syntax: syntaxVimCursor, blocking: []string{"-f", "--nofork"}}
	case name == "nvim":
		return editorKind{style: styleNeovim, syntax: syntaxVimCursor}
	case name == "vim":
		return editorKind{style: styleTerminal, syntax: syntaxVimCursor}
	case name == "vi" || name == "view":
		return editorKind{style: styleTerminal, syntax: syntaxPlusLine}
	case name == "nano":
		return editorKind{style: styleTerminal, syntax: syntaxPlusComma}
	case name == "micro" || name == "kak":
		return editorKind{style: styleTerminal, syntax: syntaxPlusColon}
	case name == "hx" || name == "helix":
		return editorKind{style: styleTerminal, syntax: syntaxColon}
	}
	return editorKind{style: styleTerminal, syntax: syntaxFile}
}

// ResolveOpener returns the opener of Ctrl+click: dev.picker.editor when it
// is set, else the editor of ktml dev's own $VISUAL, then $EDITOR. It
// returns nil when there is none, and an error, naming the variable, when
// the variable cannot be split into words without a shell. getenv reads
// ktml dev's environment.
func ResolveOpener(configured []string, getenv func(string) string) (*Opener, error) {
	if len(configured) > 0 {
		return &Opener{Source: "dev.picker.editor", Name: filepath.Base(configured[0]), words: configured, style: styleCommand, getenv: getenv}, nil
	}
	for _, variable := range []string{"VISUAL", "EDITOR"} {
		value := strings.TrimSpace(getenv(variable))
		if value == "" {
			continue
		}
		words, err := splitWords(value)
		if err != nil {
			return nil, fmt.Errorf("$%s (%q) is not a command ktml dev can run without a shell: %v; set dev.picker.editor instead", variable, value, err)
		}
		name := filepath.Base(words[0])
		kind := kindOf(name)
		opener := &Opener{Source: "$" + variable, Name: name, style: kind.style, syntax: kind.syntax, getenv: getenv}
		opener.words = []string{words[0]}
		for _, word := range words[1:] {
			if slices.Contains(kind.terminalFlags, word) {
				opener.style = styleTerminal
				if kind.syntax == syntaxEmacsclient {
					opener.syntax = syntaxPlusColon
				}
			}
			if !slices.Contains(kind.blocking, word) {
				opener.words = append(opener.words, word)
			}
		}
		return opener, nil
	}
	return nil, nil
}

// Describe says how the opener opens a source, for ktml dev --dry-run.
func (o *Opener) Describe() string {
	command := strings.Join(o.words, " ")
	switch o.style {
	case styleCommand:
		return command + " (dev.picker.editor)"
	case styleGUI:
		return fmt.Sprintf("%s (from %s): runs %s", o.Name, o.Source, strings.Join(o.argv("FILE", 0, 0), " "))
	}
	terminal := "no terminal found, so Ctrl+click copies file:line:column (install xdg-terminal-exec or set $TERMINAL)"
	if prefix, _ := findTerminal(o.getenv); prefix != nil {
		terminal = "a new terminal window (" + filepath.Base(prefix[0]) + ")"
	}
	if o.style == styleNeovim {
		return fmt.Sprintf("%s (from %s): the running Neovim whose working directory holds the file, as the KTML Neovim plugin registers it, else %s in %s", o.Name, o.Source, command, terminal)
	}
	return fmt.Sprintf("%s (from %s): runs %s in %s", o.Name, o.Source, strings.Join(o.argv("FILE", 0, 0), " "), terminal)
}

// argv is the editor's command for a file and position; line 0 spells
// them as LINE and COLUMN.
func (o *Opener) argv(file string, line, column int) []string {
	l, c := strconv.Itoa(line), strconv.Itoa(column)
	if line == 0 {
		l, c = "LINE", "COLUMN"
	}
	args := slices.Clone(o.words)
	switch o.syntax {
	case syntaxGotoFlag:
		return append(args, "-g", file+":"+l+":"+c)
	case syntaxColon:
		return append(args, file+":"+l+":"+c)
	case syntaxJetBrains:
		return append(args, "--line", l, "--column", c, file)
	case syntaxEmacsclient:
		return append(args, "-n", "+"+l+":"+c, file)
	case syntaxPlusColon:
		return append(args, "+"+l+":"+c, file)
	case syntaxPlusComma:
		return append(args, "+"+l+","+c, file)
	case syntaxPlusLine:
		return append(args, "+"+l, file)
	case syntaxVimCursor:
		return append(args, "+call cursor("+l+","+c+")", file)
	}
	return append(args, file)
}

// CopyFallback is an Editor's error when no editor can open the source, so
// the picker copies file:line:column instead and shows the reason.
type CopyFallback struct {
	Reason string
}

func (f *CopyFallback) Error() string { return f.Reason }

// Editor opens sources in root with the opener. It opens only existing
// .ktml Documents inside root, and never runs a shell.
func (o *Opener) Editor(root string) Editor {
	return func(location explorer.Location) error {
		if len(o.words) == 0 {
			return errors.New("no editor is configured: set dev.picker.editor or $EDITOR")
		}
		file, err := checkLocation(root, location)
		if err != nil {
			return err
		}
		var command []string
		switch o.style {
		case styleCommand:
			replacer := strings.NewReplacer("{file}", file, "{line}", strconv.Itoa(location.Line), "{column}", strconv.Itoa(location.Column))
			for _, arg := range o.words {
				command = append(command, replacer.Replace(arg))
			}
		case styleGUI:
			command = o.argv(file, location.Line, location.Column)
		case styleNeovim:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := openInNeovim(ctx, o.words[0], o.getenv, file, location.Line, location.Column)
			cancel()
			if err == nil {
				return nil
			}
			slog.Debug("No running Neovim opened the source; starting nvim in a terminal", "error", err)
			fallthrough
		case styleTerminal:
			prefix, err := findTerminal(o.getenv)
			if err != nil {
				return &CopyFallback{Reason: fmt.Sprintf("%s from %s needs a terminal, but %v", o.Name, o.Source, err)}
			}
			command = append(prefix, o.argv(file, location.Line, location.Column)...)
		}
		process := exec.Command(command[0], command[1:]...)
		process.Dir = root
		if err := startCommand(process); err != nil {
			return fmt.Errorf("start the editor %s: %w", command[0], err)
		}
		return nil
	}
}

// checkLocation returns the absolute path of a located source, without
// symbolic links, when it is an existing .ktml Document inside root.
func checkLocation(root string, location explorer.Location) (string, error) {
	file, err := filepath.EvalSymlinks(location.Path)
	if err == nil {
		file, err = filepath.Abs(file)
	}
	if err != nil {
		return "", fmt.Errorf("the picker cannot open %s: %w", location.File, err)
	}
	project := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		project = resolved
	}
	relative, err := filepath.Rel(project, file)
	if err != nil || !filepath.IsLocal(relative) || filepath.Ext(file) != ".ktml" || location.Line < 1 || location.Column < 1 {
		return "", fmt.Errorf("the picker only opens KTML Documents inside the project, not %s", location.File)
	}
	return file, nil
}
