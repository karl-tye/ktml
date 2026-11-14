package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/runner"
)

func TestRunner_Start(t *testing.T) {
	cmd := []string{"sleep", "1"}
	r := runner.New(cmd)

	ctx := context.Background()
	err := r.Start(ctx)
	if err != nil {
		t.Fatalf("expected nil error on start, got %v", err)
	}

	if !r.IsRunning() {
		t.Fatalf("expected runner to be running")
	}

	r.Stop()
}

func TestRunner_Stop_Cleanly(t *testing.T) {
	cmd := []string{"sleep", "10"}
	r := runner.New(cmd)

	ctx := context.Background()
	_ = r.Start(ctx)

	err := r.Stop()
	if err != nil {
		t.Fatalf("expected nil error on stop, got %v", err)
	}

	if r.IsRunning() {
		t.Fatalf("expected runner to not be running")
	}
}

func TestRunner_Stop_Forcefully(t *testing.T) {
	cmd := []string{"sh", "-c", "trap '' TERM; sleep 10"}
	r := runner.New(cmd)

	ctx := context.Background()
	_ = r.Start(ctx)

	err := r.Stop()
	if err != nil {
		t.Fatalf("expected nil error on stop, got %v", err)
	}

	if r.IsRunning() {
		t.Fatalf("expected runner to not be running")
	}
}

func TestRunner_Restart(t *testing.T) {
	cmd := []string{"sleep", "10"}
	r := runner.New(cmd)

	ctx := context.Background()
	_ = r.Start(ctx)

	err := r.Restart(ctx, nil)
	if err != nil {
		t.Fatalf("expected nil error on restart, got %v", err)
	}

	if !r.IsRunning() {
		t.Fatalf("expected runner to be running after restart")
	}

	r.Stop()
}

type buildRunnerFixture struct {
	dir    string
	source string
	log    string
	runner *runner.Runner
}

// newBuildRunnerFixture builds by copying a shell script into the fresh binary
// path. Each running binary appends its version marker to the log.
func newBuildRunnerFixture(t *testing.T) *buildRunnerFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := &buildRunnerFixture{
		dir:    dir,
		source: filepath.Join(dir, "server.sh"),
		log:    filepath.Join(dir, "starts"),
	}
	fixture.writeServer(t, "v1", true)
	fixture.runner = runner.NewBuild(
		[]string{"sh", "-c", `test -f "$1" && cp "$1" "$2" && chmod +x "$2"`, "build", fixture.source, runner.BinaryPlaceholder},
		[]string{runner.BinaryPlaceholder, fixture.log},
		filepath.Join(dir, "bin"),
	)
	return fixture
}

func (f *buildRunnerFixture) writeServer(t *testing.T, version string, healthy bool) {
	t.Helper()
	body := "echo " + version + " >> \"$1\"\nexec sleep 30\n"
	if !healthy {
		body = "echo " + version + " >> \"$1\"\nexit 1\n"
	}
	if err := os.WriteFile(f.source, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write server source: %v", err)
	}
}

func (f *buildRunnerFixture) starts(t *testing.T) []string {
	t.Helper()
	content, err := os.ReadFile(f.log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read start log: %v", err)
	}
	return strings.Fields(string(content))
}

func (f *buildRunnerFixture) readyWhenLogged(version string) runner.ReadyFunc {
	return func(ctx context.Context) error {
		for {
			if content, err := os.ReadFile(f.log); err == nil && strings.Contains(string(content), version) {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
}

func (f *buildRunnerFixture) waitForStarts(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(f.starts(t)) < want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(f.starts(t)); got < want {
		t.Fatalf("starts = %d, want %d", got, want)
	}
}

func TestRunner_GIVENBuildFails_WHENRestarting_THENKeepsCurrentProcessRunning(t *testing.T) {
	fixture := newBuildRunnerFixture(t)
	if err := fixture.runner.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = fixture.runner.Stop() })
	fixture.waitForStarts(t, 1)
	working := fixture.runner.Binary()
	if err := os.Remove(fixture.source); err != nil {
		t.Fatalf("break build: %v", err)
	}

	err := fixture.runner.Restart(context.Background(), fixture.readyWhenLogged("v2"))

	if err == nil {
		t.Fatal("Restart() succeeded although the build failed")
	}
	if !fixture.runner.IsRunning() {
		t.Fatal("failed build stopped the working backend")
	}
	if got := fixture.starts(t); !slices.Equal(got, []string{"v1"}) {
		t.Errorf("starts = %v, want only the original v1", got)
	}
	if fixture.runner.Binary() != working {
		t.Errorf("Binary() = %q, want working %q", fixture.runner.Binary(), working)
	}
}

func TestRunner_GIVENReplacementExitsBeforeReady_WHENRestarting_THENRestartsPreviousBinaryAndReportsFailure(t *testing.T) {
	fixture := newBuildRunnerFixture(t)
	if err := fixture.runner.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = fixture.runner.Stop() })
	fixture.waitForStarts(t, 1)
	working := fixture.runner.Binary()
	fixture.writeServer(t, "v2", false)

	err := fixture.runner.Restart(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	if err == nil {
		t.Fatal("Restart() succeeded although the replacement exited")
	}
	fixture.waitForStarts(t, 3)
	if got := fixture.starts(t); !slices.Equal(got, []string{"v1", "v2", "v1"}) {
		t.Errorf("starts = %v, want v1, failed v2, restored v1", got)
	}
	if !fixture.runner.IsRunning() {
		t.Fatal("previous working backend was not restarted")
	}
	if fixture.runner.Binary() != working {
		t.Errorf("Binary() = %q, want previous %q", fixture.runner.Binary(), working)
	}
	if _, err := os.Stat(working); err != nil {
		t.Errorf("previous working binary was removed: %v", err)
	}
}

func TestRunner_GIVENReplacementNeverReady_WHENRestarting_THENRestartsPreviousBinary(t *testing.T) {
	fixture := newBuildRunnerFixture(t)
	if err := fixture.runner.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = fixture.runner.Stop() })
	fixture.waitForStarts(t, 1)
	working := fixture.runner.Binary()
	fixture.writeServer(t, "v2", true)
	neverReady := fixture.readyWhenLogged("never")

	err := fixture.runner.Restart(context.Background(), func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		return neverReady(ctx)
	})

	if err == nil {
		t.Fatal("Restart() succeeded although the replacement never became ready")
	}
	fixture.waitForStarts(t, 3)
	if got := fixture.starts(t); !slices.Equal(got, []string{"v1", "v2", "v1"}) {
		t.Errorf("starts = %v, want v1, unready v2, restored v1", got)
	}
	if fixture.runner.Binary() != working {
		t.Errorf("Binary() = %q, want previous %q", fixture.runner.Binary(), working)
	}
}

func TestRunner_GIVENReadyReplacement_WHENRestarting_THENSwapsBinaryAndDropsPrevious(t *testing.T) {
	fixture := newBuildRunnerFixture(t)
	if err := fixture.runner.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = fixture.runner.Stop() })
	fixture.waitForStarts(t, 1)
	previous := fixture.runner.Binary()
	fixture.writeServer(t, "v2", true)

	if err := fixture.runner.Restart(context.Background(), fixture.readyWhenLogged("v2")); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}

	if got := fixture.starts(t); !slices.Equal(got, []string{"v1", "v2"}) {
		t.Errorf("starts = %v, want v1 then v2", got)
	}
	if current := fixture.runner.Binary(); current == previous || current == "" {
		t.Errorf("Binary() = %q, want a fresh binary distinct from %q", current, previous)
	}
	if _, err := os.Stat(previous); !os.IsNotExist(err) {
		t.Errorf("superseded binary still exists: %v", err)
	}
}

func TestRunner_GIVENEnvironment_WHENStarted_THENTheBackendSeesIt(t *testing.T) {
	output := filepath.Join(t.TempDir(), "env")
	r := runner.New([]string{"sh", "-c", `printf '%s %s' "$KTML_DEV_TOKEN" "$KTML_DEV_ROOT" > "$1"; exec sleep 30`, "runner", output})
	r.SetEnv("KTML_DEV_TOKEN=secret", "KTML_DEV_ROOT=/workspace")
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(output); err == nil && len(content) > 0 {
			if string(content) != "secret /workspace" {
				t.Errorf("backend environment = %q", content)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("backend did not report its environment")
}

func TestBuildsDevelopmentVariant_GIVENBuildCommands_WHENChecked_THENRequiresTheKtmldevTag(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"go", "build", "-tags", "ktmldev", "-o", "{binary}", "./cmd/app"}, true},
		{[]string{"go", "build", "-tags=netgo,ktmldev", "./cmd/app"}, true},
		{[]string{"go", "build", "--tags", "integration ktmldev", "./cmd/app"}, true},
		{[]string{"go", "build", "-o", "{binary}", "./cmd/app"}, false},
		{[]string{"go", "build", "-tags", "ktmldevx", "./cmd/app"}, false},
		{[]string{"go", "build", "-tags"}, false},
	}
	for _, tt := range tests {
		if got := runner.BuildsDevelopmentVariant(tt.args); got != tt.want {
			t.Errorf("BuildsDevelopmentVariant(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
