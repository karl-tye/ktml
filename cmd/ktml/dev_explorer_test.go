package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// devExplorerProject writes a Go module with one component and its story,
// and a dev config whose backend is a long-running placeholder process.
// explorer is the dev.explorer member, or "" for none.
func devExplorerProject(t *testing.T, devPort int, explorer string) string {
	t.Helper()
	dir := t.TempDir()
	extra := ""
	if explorer != "" {
		extra = `, "explorer": ` + explorer
	}
	files := map[string]string{
		"go.mod":             "module example.com/devexplorer\n\ngo 1.24\n",
		"ui/card.ktml":       "Card [title: string] {\n\th2 \"{title}\"\n}\n",
		"ui/card.story.ktml": "import { Card } from \"./card.ktml\"\n\nstory Card.Basic [title = \"Hello\"]\n",
		"ktml.config.json":   fmt.Sprintf(`{"dev": {"proxy_target": "http://127.0.0.1:1", "command": ["sleep", "60"], "port": %d%s}}`, devPort, extra),
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func buildKTML(t *testing.T) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "ktml")
	build := exec.Command("go", "build", "-o", executable, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return executable
}

func ephemeralPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve ephemeral port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// lockedBuffer collects a process's output while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startDev runs ktml dev in dir and returns its combined output and a
// channel closed when it exits. The test interrupts it on cleanup.
func startDev(t *testing.T, executable, dir string) (*exec.Cmd, *lockedBuffer, <-chan error) {
	t.Helper()
	output := &lockedBuffer{}
	command := exec.Command(executable, "dev")
	command.Dir = dir
	// The tests wait for debug records, which the CLI logs only on request.
	command.Env = append(os.Environ(), "KTML_LOG_LEVEL=debug")
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		t.Fatalf("start ktml dev: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
	})
	return command, output, exited
}

// waitFor polls condition until it holds, failing with the dev output after
// the deadline.
func waitFor(t *testing.T, output *lockedBuffer, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s\nktml dev output:\n%s", what, output)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func httpGet(address string) (int, string, error) {
	response, err := http.Get(address)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response.StatusCode, string(body), err
}

func interruptDev(t *testing.T, command *exec.Cmd, output *lockedBuffer, exited <-chan error) {
	t.Helper()
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt ktml dev: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatalf("ktml dev did not stop after an interrupt\n%s", output)
	}
}

func TestKTMLDev_GIVENDevExplorer_WHENStarted_THENServesTheExplorerAndStopsWithDev(t *testing.T) {
	executable := buildKTML(t)
	explorerAddr := fmt.Sprintf("127.0.0.1:%d", ephemeralPort(t))
	dir := devExplorerProject(t, ephemeralPort(t), `{"addr": "`+explorerAddr+`"}`)
	command, output, exited := startDev(t, executable, dir)

	var status int
	var body string
	waitFor(t, output, "the Explorer", func() bool {
		var err error
		status, body, err = httpGet("http://" + explorerAddr + "/")
		return err == nil
	})

	if status != http.StatusOK || !strings.Contains(body, `?story=ui%2Fcard.story.ktml%23Card.Basic`) {
		t.Fatalf("GET Explorer = %d, want the index listing the story\n%s", status, body)
	}
	waitFor(t, output, "the Explorer URL in the log", func() bool {
		return strings.Contains(output.String(), "KTML Explorer on http://"+explorerAddr+"/")
	})
	interruptDev(t, command, output, exited)
	if _, err := net.DialTimeout("tcp", explorerAddr, time.Second); err == nil {
		t.Errorf("the Explorer still accepts connections on %s after ktml dev stopped", explorerAddr)
	}
}

func TestKTMLDev_GIVENNoDevExplorer_WHENStarted_THENStartsNoExplorer(t *testing.T) {
	executable := buildKTML(t)
	devPort := ephemeralPort(t)
	dir := devExplorerProject(t, devPort, "")
	command, output, exited := startDev(t, executable, dir)

	waitFor(t, output, "the dev server", func() bool {
		_, _, err := httpGet(fmt.Sprintf("http://127.0.0.1:%d/", devPort))
		return err == nil && strings.Contains(output.String(), "Starting hub listen loop")
	})

	if strings.Contains(output.String(), "KTML Explorer on") {
		t.Errorf("ktml dev without dev.explorer mentions the Explorer:\n%s", output)
	}
	interruptDev(t, command, output, exited)
}

func TestKTMLDev_GIVENNonLoopbackDevExplorer_WHENStarted_THENExitsWithTheConfigError(t *testing.T) {
	executable := buildKTML(t)
	dir := devExplorerProject(t, ephemeralPort(t), `{"addr": "0.0.0.0:0"}`)
	command := exec.Command(executable, "dev")
	command.Dir = dir

	output, err := command.CombinedOutput()

	if err == nil || !strings.Contains(string(output), "dev explorer addr") || !strings.Contains(string(output), "loopback") {
		t.Fatalf("ktml dev error = %v, want a loopback config error\n%s", err, output)
	}
}
