package picker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/explorer"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// TestSplitWords_GIVENAnEditorVariable_WHENSplit_THENHonoursQuotesAndRefusesWhatNeedsAShell
// splits as a shell would, without expanding anything.
func TestSplitWords_GIVENAnEditorVariable_WHENSplit_THENHonoursQuotesAndRefusesWhatNeedsAShell(t *testing.T) {
	for _, test := range []struct {
		value string
		want  []string
	}{
		{"nvim", []string{"nvim"}},
		{"  code   --wait ", []string{"code", "--wait"}},
		{`"/opt/My Editor/bin/edit" -x`, []string{"/opt/My Editor/bin/edit", "-x"}},
		{`'/opt/it'\''s/vim'`, []string{"/opt/it's/vim"}},
		{`/opt/My\ Editor/edit`, []string{"/opt/My Editor/edit"}},
		{`emacsclient -a "" -c`, []string{"emacsclient", "-a", "", "-c"}},
		{`edit "a \"b\" \$c"`, []string{"edit", `a "b" $c`}},
	} {
		got, err := splitWords(test.value)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("splitWords(%q) = %q, %v; want %q", test.value, got, err, test.want)
		}
	}
	for _, value := range []string{"", "   ", `"unclosed`, `'unclosed`, `vim \`, `vim; rm x`, "vim | tee", "$HOME/bin/vim", "`which vim`", `vim "$X"`, "vim > out", `"" -x`} {
		if got, err := splitWords(value); err == nil {
			t.Errorf("splitWords(%q) = %q, want an error", value, got)
		}
	}
}

// TestResolveOpener_GIVENEditorVariables_WHENResolved_THENEachEditorGetsItsGotoSyntax
// is the resolution table: dev.picker.editor wins, then $VISUAL, then
// $EDITOR, and the program's base name picks how the file opens.
func TestResolveOpener_GIVENEditorVariables_WHENResolved_THENEachEditorGetsItsGotoSyntax(t *testing.T) {
	const file = "/p/web/a b.ktml"
	for _, test := range []struct {
		editor string
		style  style
		argv   []string
	}{
		{"code --wait", styleGUI, []string{"code", "-g", file + ":12:3"}},
		{"code -w --new-window", styleGUI, []string{"code", "--new-window", "-g", file + ":12:3"}},
		{"/usr/bin/codium", styleGUI, []string{"/usr/bin/codium", "-g", file + ":12:3"}},
		{"cursor", styleGUI, []string{"cursor", "-g", file + ":12:3"}},
		{"windsurf", styleGUI, []string{"windsurf", "-g", file + ":12:3"}},
		{"zed --wait", styleGUI, []string{"zed", file + ":12:3"}},
		{"subl -w", styleGUI, []string{"subl", file + ":12:3"}},
		{"goland --wait", styleGUI, []string{"goland", "--line", "12", "--column", "3", file}},
		{"idea.sh", styleGUI, []string{"idea.sh", "--line", "12", "--column", "3", file}},
		{"webstorm", styleGUI, []string{"webstorm", "--line", "12", "--column", "3", file}},
		{"emacsclient -c", styleGUI, []string{"emacsclient", "-c", "-n", "+12:3", file}},
		{"emacsclient -n", styleGUI, []string{"emacsclient", "-n", "+12:3", file}},
		{"emacsclient -t", styleTerminal, []string{"emacsclient", "-t", "+12:3", file}},
		{"emacs", styleGUI, []string{"emacs", "+12:3", file}},
		{"emacs -nw", styleTerminal, []string{"emacs", "-nw", "+12:3", file}},
		{"gvim -f", styleGUI, []string{"gvim", "+call cursor(12,3)", file}},
		{"nvim", styleNeovim, []string{"nvim", "+call cursor(12,3)", file}},
		{"/usr/local/bin/nvim -u NONE", styleNeovim, []string{"/usr/local/bin/nvim", "-u", "NONE", "+call cursor(12,3)", file}},
		{"vim", styleTerminal, []string{"vim", "+call cursor(12,3)", file}},
		{"vi", styleTerminal, []string{"vi", "+12", file}},
		{"nano", styleTerminal, []string{"nano", "+12,3", file}},
		{"micro", styleTerminal, []string{"micro", "+12:3", file}},
		{"kak", styleTerminal, []string{"kak", "+12:3", file}},
		{"hx", styleTerminal, []string{"hx", file + ":12:3"}},
		{"helix", styleTerminal, []string{"helix", file + ":12:3"}},
		{"ed", styleTerminal, []string{"ed", file}},
		{"gedit", styleTerminal, []string{"gedit", file}},
	} {
		opener, err := ResolveOpener(nil, env(map[string]string{"EDITOR": test.editor}))
		if err != nil || opener == nil {
			t.Errorf("%q: %v, %v", test.editor, opener, err)
			continue
		}
		if opener.Source != "$EDITOR" || opener.style != test.style || !reflect.DeepEqual(opener.argv(file, 12, 3), test.argv) {
			t.Errorf("%q: source %s style %d argv %q; want style %d argv %q", test.editor, opener.Source, opener.style, opener.argv(file, 12, 3), test.style, test.argv)
		}
	}

	visual, err := ResolveOpener(nil, env(map[string]string{"VISUAL": "code", "EDITOR": "nvim"}))
	if err != nil || visual.Source != "$VISUAL" || visual.Name != "code" {
		t.Errorf("$VISUAL first: %+v, %v", visual, err)
	}
	configured, err := ResolveOpener([]string{"zed", "{file}:{line}"}, env(map[string]string{"VISUAL": "code", "EDITOR": "nvim"}))
	if err != nil || configured.Source != "dev.picker.editor" || configured.style != styleCommand || configured.Name != "zed" {
		t.Errorf("dev.picker.editor first: %+v, %v", configured, err)
	}
	if none, err := ResolveOpener(nil, env(nil)); none != nil || err != nil {
		t.Errorf("no editor: %+v, %v", none, err)
	}
	if _, err := ResolveOpener(nil, env(map[string]string{"EDITOR": `vim "unclosed`})); err == nil || !strings.Contains(err.Error(), "$EDITOR") || !strings.Contains(err.Error(), "dev.picker.editor") {
		t.Errorf("an invalid $EDITOR: %v", err)
	}
	if _, err := ResolveOpener(nil, env(map[string]string{"VISUAL": "$(evil)", "EDITOR": "vim"})); err == nil || !strings.Contains(err.Error(), "$VISUAL") {
		t.Errorf("an invalid $VISUAL: %v", err)
	}
}

// writeProject makes a project with one KTML source and returns the
// project, the source, and a location of line 12, column 3 in it.
func writeProject(t *testing.T, name string) (string, string, explorer.Location) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "web", name)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, source, explorer.Location{File: "web/" + name, Path: source, Line: 12, Column: 3}
}

// fakeProgram writes an executable that writes its path and arguments, one
// per line, to record.
func fakeProgram(t *testing.T, dir, name, record string) {
	t.Helper()
	mv, err := exec.LookPath("mv")
	if err != nil {
		t.Skip("skipping: no mv")
	}
	script := "#!/bin/sh\nfor a in \"$0\" \"$@\"; do printf '%s\\n' \"$a\"; done > '" + record + ".tmp' && '" + mv + "' '" + record + ".tmp' '" + record + "'\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitRecord(t *testing.T, record string) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(record); err == nil {
			_ = os.Remove(record)
			return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never written", record)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestOpenerEditor_GIVENTerminalEditors_WHENOpened_THENRunsThemInANewTerminalOrFallsBackToCopying
// runs a terminal editor with xdg-terminal-exec, else $TERMINAL with its
// flag, and asks the picker to copy when there is no terminal; a GUI
// editor runs directly. The executables are fakes on PATH.
func TestOpenerEditor_GIVENTerminalEditors_WHENOpened_THENRunsThemInANewTerminalOrFallsBackToCopying(t *testing.T) {
	root, source, location := writeProject(t, "board.ktml")
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "args")
	for _, name := range []string{"xdg-terminal-exec", "alacritty", "foot", "code"} {
		fakeProgram(t, bin, name, record)
	}
	t.Setenv("PATH", bin)
	open := func(values map[string]string) error {
		t.Helper()
		opener, err := ResolveOpener(nil, env(values))
		if err != nil {
			t.Fatal(err)
		}
		return opener.Editor(root)(location)
	}

	if err := open(map[string]string{"EDITOR": "vim"}); err != nil {
		t.Fatal(err)
	}
	if got := waitRecord(t, record); !reflect.DeepEqual(got, []string{filepath.Join(bin, "xdg-terminal-exec"), "vim", "+call cursor(12,3)", source}) {
		t.Errorf("xdg-terminal-exec ran %q", got)
	}
	if err := open(map[string]string{"EDITOR": "code --wait"}); err != nil {
		t.Fatal(err)
	}
	if got := waitRecord(t, record); !reflect.DeepEqual(got, []string{filepath.Join(bin, "code"), "-g", source + ":12:3"}) {
		t.Errorf("code ran %q", got)
	}
	// nvim with no running instance registered runs in a terminal; the
	// registry is empty.
	if err := open(map[string]string{"EDITOR": "nvim", "XDG_RUNTIME_DIR": t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if got := waitRecord(t, record); !reflect.DeepEqual(got, []string{filepath.Join(bin, "xdg-terminal-exec"), "nvim", "+call cursor(12,3)", source}) {
		t.Errorf("nvim without a server ran %q", got)
	}

	if err := os.Remove(filepath.Join(bin, "xdg-terminal-exec")); err != nil {
		t.Fatal(err)
	}
	if err := open(map[string]string{"EDITOR": "hx", "TERMINAL": "alacritty"}); err != nil {
		t.Fatal(err)
	}
	if got := waitRecord(t, record); !reflect.DeepEqual(got, []string{filepath.Join(bin, "alacritty"), "-e", "hx", source + ":12:3"}) {
		t.Errorf("$TERMINAL ran %q", got)
	}
	if err := open(map[string]string{"EDITOR": "nano", "TERMINAL": "foot"}); err != nil {
		t.Fatal(err)
	}
	if got := waitRecord(t, record); !reflect.DeepEqual(got, []string{filepath.Join(bin, "foot"), "nano", "+12,3", source}) {
		t.Errorf("foot ran %q", got)
	}

	for _, values := range []map[string]string{
		{"EDITOR": "vim"},
		{"EDITOR": "vim", "TERMINAL": "missing-terminal"},
		{"EDITOR": "vim", "TERMINAL": "term; rm"},
	} {
		err := open(values)
		var fallback *CopyFallback
		if !errors.As(err, &fallback) || !strings.Contains(fallback.Reason, "vim from $EDITOR needs a terminal") {
			t.Errorf("%v: %v, want a copy fallback", values, err)
		}
	}
	opener, _ := ResolveOpener(nil, env(map[string]string{"EDITOR": "vim"}))
	if description := opener.Describe(); !strings.Contains(description, "Ctrl+click copies") {
		t.Errorf("Describe without a terminal: %s", description)
	}
	// The project rules hold for every opener.
	if err := opener.Editor(root)(explorer.Location{File: "/etc/passwd", Path: "/etc/passwd", Line: 1, Column: 1}); err == nil || errors.As(err, new(*CopyFallback)) {
		t.Errorf("opened /etc/passwd: %v", err)
	}
}

// registerNeovim writes a registry entry as the plugin does.
func registerNeovim(t *testing.T, dir string, pid int, server, cwd string, modified time.Time) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"pid": pid, "server": server, "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strconv.Itoa(pid)+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

// deadPID is the process ID of a process that has exited.
func deadPID(t *testing.T) int {
	t.Helper()
	command := exec.Command("true")
	if err := command.Run(); err != nil {
		t.Skip("skipping: cannot run true")
	}
	return command.Process.Pid
}

// TestNeovimServers_GIVENARegistry_WHENAFileOpens_THENPrefersNVIMThenTheDeepestLiveInstanceHoldingIt
// reads the plugin's registry: $NVIM first, then the instances whose
// working directory holds the file, deepest and then newest first; the
// files of exited instances are removed, and a registry that others can
// write is not read.
func TestNeovimServers_GIVENARegistry_WHENAFileOpens_THENPrefersNVIMThenTheDeepestLiveInstanceHoldingIt(t *testing.T) {
	runtime := t.TempDir()
	dir := filepath.Join(runtime, "ktml", "nvim")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	project, source, _ := writeProject(t, "board.ktml")
	// Unix socket paths are short.
	sockets, err := os.MkdirTemp("/tmp", "ktml-picker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	socket := func(name string) string {
		path := filepath.Join(sockets, name)
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		return path
	}
	now := time.Now()
	// Live entries use this test's own process ID under different names.
	self := os.Getpid()
	shallow := socket("shallow")
	registerNeovim(t, dir, self, shallow, filepath.Dir(project), now)
	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Skip("skipping: cannot run sleep")
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })
	deep := socket("deep")
	registerNeovim(t, dir, sleeper.Process.Pid, deep, project, now.Add(-time.Hour))
	older := socket("older")
	registerNeovim(t, dir, os.Getppid(), older, project, now.Add(-2*time.Hour))
	dead := registerNeovim(t, dir, deadPID(t), socket("dead"), project, now)
	gone := registerNeovim(t, dir, self+1000000, filepath.Join(sockets, "missing"), project, now)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := env(map[string]string{"XDG_RUNTIME_DIR": runtime, "NVIM": "/run/own.sock"})

	servers := neovimServers(getenv, source)
	if !reflect.DeepEqual(servers, []string{"/run/own.sock", deep, older, shallow}) {
		t.Errorf("servers = %q", servers)
	}
	for _, path := range []string{dead, gone} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the stale entry %s was not removed", path)
		}
	}
	if servers := neovimServers(env(map[string]string{"XDG_RUNTIME_DIR": runtime}), "/elsewhere/x.ktml"); len(servers) != 0 {
		t.Errorf("servers for a file outside every cwd = %q", servers)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if servers := neovimServers(env(map[string]string{"XDG_RUNTIME_DIR": runtime}), source); len(servers) != 0 {
		t.Errorf("read a registry others can write: %q", servers)
	}
	if got := neovimRegistry(env(map[string]string{"TMPDIR": "/var/tmp"})); got != filepath.Join("/var/tmp", "ktml-"+strconv.Itoa(os.Getuid()), "nvim") {
		t.Errorf("registry without XDG_RUNTIME_DIR = %s", got)
	}
}

// TestOpenInNeovim_GIVENAPathWithShellAndVimSpecials_WHENOpened_THENThePathTravelsOnlyAsHexDigits
// calls the plugin with the path hex-encoded and the position as numbers,
// so nothing in the path can become a command, and tries the next server
// when one fails.
func TestOpenInNeovim_GIVENAPathWithShellAndVimSpecials_WHENOpened_THENThePathTravelsOnlyAsHexDigits(t *testing.T) {
	runtime := t.TempDir()
	dir := filepath.Join(runtime, "ktml", "nvim")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	file := filepath.Join(project, "web", "it's a |%\" <cr> `x` $(y).ktml")
	var calls [][]string
	previous := runRemote
	runRemote = func(_ context.Context, args []string) ([]byte, error) {
		calls = append(calls, args)
		if args[2] == "/run/first.sock" {
			return nil, errors.New("connection refused")
		}
		return []byte("opened"), nil
	}
	t.Cleanup(func() { runRemote = previous })
	getenv := env(map[string]string{"XDG_RUNTIME_DIR": runtime, "NVIM": "/run/first.sock"})
	if err := openInNeovim(context.Background(), "nvim", getenv, file, 12, 3); err == nil {
		t.Fatal("opened with only a failing server")
	}
	registerNeovim(t, dir, os.Getpid(), "127.0.0.1:6666", project, time.Now())
	calls = nil
	if err := openInNeovim(context.Background(), "nvim", getenv, file, 12, 3); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1][2] != "127.0.0.1:6666" || calls[1][0] != "nvim" || calls[1][1] != "--server" || calls[1][3] != "--remote-expr" {
		t.Fatalf("calls = %q", calls)
	}
	expression := calls[1][4]
	encoded := hex.EncodeToString([]byte(file))
	if !strings.HasSuffix(expression, ", ['"+encoded+"', 12, 3])") {
		t.Errorf("expression = %s", expression)
	}
	// Apart from the fixed code, the expression holds only hex digits and
	// numbers.
	if rest := strings.TrimSuffix(expression, "['"+encoded+"', 12, 3])"); !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(encoded) || strings.Contains(rest, "it's") || rest != remoteOpenPrefix() {
		t.Errorf("the path leaked into the code: %s", expression)
	}
	runRemote = func(context.Context, []string) ([]byte, error) { return []byte("the KTML plugin is not loaded"), nil }
	if err := openInNeovim(context.Background(), "nvim", getenv, file, 12, 3); err == nil || !strings.Contains(err.Error(), "not loaded") {
		t.Errorf("a Neovim without the plugin: %v", err)
	}
}

// remoteOpenPrefix is remoteOpen up to its data.
func remoteOpenPrefix() string {
	prefix, _, _ := strings.Cut(strings.ReplaceAll(remoteOpen, "%%", "%"), "['%s'")
	return prefix
}
