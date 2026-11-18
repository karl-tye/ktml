package picker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The KTML Neovim plugin (editors/nvim) registers each running Neovim in a
// per-user runtime directory, one file per instance named by its process
// ID: <pid>.json holding {"pid", "server", "cwd"}, the instance's
// v:servername and global working directory. The directory is
// $XDG_RUNTIME_DIR/ktml/nvim, or ktml-<uid>/nvim in $TMPDIR (else /tmp)
// without one; the plugin creates it with mode 0700 and each file with
// mode 0600, rewrites its file when the working directory changes, and
// removes it when Neovim exits.

// neovimEntry is one registered Neovim.
type neovimEntry struct {
	PID    int    `json:"pid"`
	Server string `json:"server"`
	Cwd    string `json:"cwd"`
	// modified orders instances with the same working directory, most
	// recently registered first.
	modified time.Time
}

// neovimRegistry is the directory of registered Neovim instances.
func neovimRegistry(getenv func(string) string) string {
	if runtime := getenv("XDG_RUNTIME_DIR"); runtime != "" {
		return filepath.Join(runtime, "ktml", "nvim")
	}
	temp := getenv("TMPDIR")
	if temp == "" {
		temp = "/tmp"
	}
	return filepath.Join(temp, "ktml-"+strconv.Itoa(os.Getuid()), "nvim")
}

var entryName = regexp.MustCompile(`^([1-9][0-9]*)\.json$`)

// neovimServers returns the servers to try for a file, in order: $NVIM,
// when ktml dev itself runs in a Neovim terminal, then each registered
// instance whose working directory holds the file, the deepest first. It
// removes the files of instances that have exited. It reads only a
// registry that this user owns and nobody else can write.
func neovimServers(getenv func(string) string, file string) []string {
	var servers []string
	if own := getenv("NVIM"); own != "" {
		servers = append(servers, own)
	}
	dir := neovimRegistry(getenv)
	if !privateDirectory(dir) {
		return servers
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		return servers
	}
	var entries []neovimEntry
	for _, name := range names {
		match := entryName.FindStringSubmatch(name.Name())
		if match == nil || !name.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, name.Name())
		info, err := name.Info()
		if err != nil || !ownedByMe(info) {
			continue
		}
		var entry neovimEntry
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &entry) != nil || strconv.Itoa(entry.PID) != match[1] || entry.Server == "" || entry.Cwd == "" {
			continue
		}
		if !processAlive(entry.PID) || !serverExists(entry.Server) {
			_ = os.Remove(path)
			continue
		}
		entry.modified = info.ModTime()
		cwd, err := filepath.EvalSymlinks(entry.Cwd)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(cwd, file)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		entry.Cwd = cwd
		entries = append(entries, entry)
	}
	slices.SortStableFunc(entries, func(a, b neovimEntry) int {
		if len(a.Cwd) != len(b.Cwd) {
			return len(b.Cwd) - len(a.Cwd)
		}
		return b.modified.Compare(a.modified)
	})
	for _, entry := range entries {
		if !slices.Contains(servers, entry.Server) {
			servers = append(servers, entry.Server)
		}
	}
	return servers
}

// serverExists reports whether a server address that names a socket file
// still exists; a TCP address is assumed to.
func serverExists(server string) bool {
	if !strings.ContainsRune(server, filepath.Separator) {
		return true
	}
	info, err := os.Stat(server)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// remoteOpen is the expression that a Neovim server evaluates to open a
// file: it calls the KTML plugin's open_location with the path as hex
// digits and the position as numbers, so that nothing from the path is
// ever read as Vim script or Lua.
const remoteOpen = `luaeval('(function(a) local ok, ktml = pcall(require, "ktml") if not ok or type(ktml.open_location) ~= "function" then return "the KTML plugin is not loaded" end return ktml.open_location((a[1]:gsub("%%x%%x", function(h) return string.char(tonumber(h, 16)) end)), a[2], a[3]) end)(_A)', ['%s', %d, %d])`

// runRemote runs `nvim --server S --remote-expr E` and returns its output;
// tests replace it.
var runRemote = func(ctx context.Context, args []string) ([]byte, error) {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdin = nil
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return output, err
}

// openInNeovim opens a file at a line and UTF-16 column in a running
// Neovim with the KTML plugin, trying each of neovimServers in turn, with
// program as the nvim client.
func openInNeovim(ctx context.Context, program string, getenv func(string) string, file string, line, column int) error {
	servers := neovimServers(getenv, file)
	if len(servers) == 0 {
		return errors.New("no running Neovim is registered for this file")
	}
	expression := fmt.Sprintf(remoteOpen, hex.EncodeToString([]byte(file)), line, column)
	var failures []string
	for _, server := range servers {
		output, err := runRemote(ctx, []string{program, "--server", server, "--remote-expr", expression})
		if err == nil && strings.TrimSpace(string(output)) == "opened" {
			return nil
		}
		if err == nil {
			err = errors.New(strings.TrimSpace(string(output)))
		}
		failures = append(failures, server+": "+err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	return errors.New(strings.Join(failures, "; "))
}
