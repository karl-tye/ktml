package picker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/explorer"
)

// TestNeovimEditor_GIVENARunningNeovimWithThePlugin_WHENASourceOpens_THENItOpensInThatNeovimAtTheColumn
// starts a headless Neovim with editors/nvim on a temporary socket and
// runtime directory: the plugin registers it, and the EDITOR=nvim opener
// opens a file whose name holds quotes, |, %, #, and spaces at a UTF-16
// column, reuses a window that already shows a file, runs the on_open
// hook, and the registration goes away when Neovim exits.
func TestNeovimEditor_GIVENARunningNeovimWithThePlugin_WHENASourceOpens_THENItOpensInThatNeovimAtTheColumn(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("skipping: nvim is not installed")
	}
	plugin, err := filepath.Abs(filepath.Join("..", "..", "..", "editors", "nvim"))
	if err != nil {
		t.Fatal(err)
	}
	// Unix socket paths are short.
	runtime, err := os.MkdirTemp("/tmp", "ktml-nvim-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtime) })
	socket := filepath.Join(runtime, "s")
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	strange := filepath.Join(project, "web", `it's a |%"x" #1.ktml`)
	other := filepath.Join(project, "web", "other.ktml")
	if err := os.MkdirAll(filepath.Dir(strange), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strange, []byte("<div>\n  <p>héllo 😀 there</p>\n</div>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("<a>\n</a>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(t.TempDir(), "hook")

	setup := `lua require("ktml").setup({ lsp = false, treesitter = false, on_open = function(info) vim.fn.writefile({ info.file, tostring(info.line) }, vim.env.KTML_HOOK) end })`
	command := exec.Command(nvim, "--clean", "--headless", "--listen", socket, "--cmd", "set rtp^="+strings.ReplaceAll(plugin, " ", `\ `), "-c", setup)
	command.Dir = project
	command.Env = slices.DeleteFunc(os.Environ(), func(variable string) bool {
		return strings.HasPrefix(variable, "NVIM=") || strings.HasPrefix(variable, "XDG_RUNTIME_DIR=")
	})
	command.Env = append(command.Env, "XDG_RUNTIME_DIR="+runtime, "KTML_HOOK="+hook)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		select {
		case <-exited:
		default:
			_ = command.Process.Kill()
			<-exited
		}
	})
	entry := filepath.Join(runtime, "ktml", "nvim", strconv.Itoa(command.Process.Pid)+".json")
	waitFor(t, "the plugin's registration", func() bool { _, err := os.Stat(entry); return err == nil })
	if info, err := os.Stat(entry); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("registration %v mode %v", err, info.Mode())
	}
	if info, err := os.Stat(filepath.Dir(entry)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("registry %v mode %v", err, info.Mode())
	}

	state := func() (name string, cursor []int, windows int) {
		t.Helper()
		output, err := exec.Command(nvim, "--server", socket, "--remote-expr", `luaeval('vim.json.encode({ name = vim.api.nvim_buf_get_name(0), cursor = vim.api.nvim_win_get_cursor(0), windows = #vim.api.nvim_tabpage_list_wins(0) })')`).Output()
		if err != nil {
			t.Fatalf("query Neovim: %v", err)
		}
		var answer struct {
			Name    string `json:"name"`
			Cursor  []int  `json:"cursor"`
			Windows int    `json:"windows"`
		}
		if err := json.Unmarshal(output, &answer); err != nil {
			t.Fatalf("query Neovim: %v: %s", err, output)
		}
		return answer.Name, answer.Cursor, answer.Windows
	}

	// No terminal can be found, so a failed open never falls back to one.
	t.Setenv("PATH", t.TempDir())
	opener, err := ResolveOpener(nil, env(map[string]string{"EDITOR": nvim, "XDG_RUNTIME_DIR": runtime}))
	if err != nil || opener.style != styleNeovim {
		t.Fatalf("opener %+v, %v", opener, err)
	}
	editor := opener.Editor(project)
	// "there" starts at UTF-16 column 15 (😀 is two code units) and byte 17.
	if err := editor(explorer.Location{File: "web/x.ktml", Path: strange, Line: 2, Column: 15}); err != nil {
		t.Fatal(err)
	}
	if name, cursor, windows := state(); name != strange || !slices.Equal(cursor, []int{2, 17}) || windows != 1 {
		t.Errorf("after the first open: %q %v %d windows", name, cursor, windows)
	}
	waitFor(t, "the on_open hook", func() bool {
		data, _ := os.ReadFile(hook)
		return string(data) == strange+"\n2\n"
	})

	// With the strange file in a second window and other.ktml current, the
	// strange file's window is reused.
	if output, err := exec.Command(nvim, "--server", socket, "--remote-expr", `execute('vsplit')`).CombinedOutput(); err != nil {
		t.Fatalf("split: %v %s", err, output)
	}
	if err := editor(explorer.Location{File: "web/other.ktml", Path: other, Line: 2, Column: 1}); err != nil {
		t.Fatal(err)
	}
	if name, cursor, windows := state(); name != other || !slices.Equal(cursor, []int{2, 0}) || windows != 2 {
		t.Errorf("other.ktml: %q %v %d windows", name, cursor, windows)
	}
	if err := editor(explorer.Location{File: "web/x.ktml", Path: strange, Line: 99, Column: 1}); err != nil {
		t.Fatal(err)
	}
	if name, cursor, windows := state(); name != strange || !slices.Equal(cursor, []int{3, 0}) || windows != 2 {
		t.Errorf("the strange file again: %q %v %d windows", name, cursor, windows)
	}

	_ = exec.Command(nvim, "--server", socket, "--remote-send", `<C-\><C-N>:qa!<CR>`).Run()
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("Neovim did not exit")
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("the registration outlived Neovim: %v", err)
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
