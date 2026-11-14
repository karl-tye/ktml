package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKTMLDev_GIVENCacheRemovalFails_WHENStarted_THENExitsBeforeStartup(t *testing.T) {
	if !removeAllPermissionFailureSupported(t) {
		t.Skip("filesystem permissions do not provide a deterministic RemoveAll failure")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	executable := filepath.Join(t.TempDir(), "ktml")
	build := exec.Command("go", "build", "-o", executable, ".")
	build.Dir = workingDirectory
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	project := t.TempDir()
	runnerMarker := filepath.Join(project, "runner-started")
	config := `{"dev":{"proxy_target":"http://127.0.0.1:1","command":["sh","-c","touch ` + runnerMarker + `"],"port":17332}}`
	if err := os.WriteFile(filepath.Join(project, "ktml.config.json"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cache := filepath.Join(project, ".ktml")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cache, "state.json"), []byte("state"), 0o600); err != nil {
		t.Fatalf("write cache file: %v", err)
	}
	if err := os.Chmod(cache, 0); err != nil {
		t.Fatalf("block cache removal: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(cache, 0o700) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "dev")
	command.Dir = project

	output, err := command.CombinedOutput()

	if ctx.Err() != nil {
		t.Fatalf("ktml dev did not exit after cache removal failure:\n%s", output)
	}
	if err == nil || !strings.Contains(string(output), "failed to clear .ktml cache") {
		t.Fatalf("ktml dev error = %v, output = %s; want cache removal failure", err, output)
	}
	if _, err := os.Stat(runnerMarker); !os.IsNotExist(err) {
		t.Fatalf("runner marker exists after cache removal failure: %v", err)
	}
}

func removeAllPermissionFailureSupported(t *testing.T) bool {
	t.Helper()
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatalf("create permission probe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "file"), []byte("probe"), 0o600); err != nil {
		t.Fatalf("write permission probe: %v", err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatalf("block permission probe: %v", err)
	}
	err := os.RemoveAll(blocked)
	if err != nil {
		_ = os.Chmod(blocked, 0o700)
	}
	return err != nil
}
