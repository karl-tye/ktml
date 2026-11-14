package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// BinaryPlaceholder is replaced in build and command arguments by the path of
// the backend binary that a build-then-run Runner produced.
const BinaryPlaceholder = "{binary}"

// ReadyFunc reports when a newly started backend can serve requests.
type ReadyFunc func(ctx context.Context) error

type Runner struct {
	cmdArgs   []string
	buildArgs []string
	binDir    string
	builds    int
	binary    string
	cmd       *exec.Cmd
	mu        sync.Mutex
	running   bool
	exited    chan struct{}
	env       []string
}

// SetEnv adds variables, as KEY=value, to the environment of every backend
// process the runner starts.
func (r *Runner) SetEnv(env ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.env = append([]string(nil), env...)
}

// BuildsDevelopmentVariant reports whether a go build command passes the
// ktmldev build tag, which links the development interpreter and registry
// (ADR-0004 decision 7). Without it every change rebuilds and reloads.
func BuildsDevelopmentVariant(buildArgs []string) bool {
	for index, arg := range buildArgs {
		var tags string
		switch {
		case arg == "-tags" || arg == "--tags":
			if index+1 < len(buildArgs) {
				tags = buildArgs[index+1]
			}
		case strings.HasPrefix(arg, "-tags=") || strings.HasPrefix(arg, "--tags="):
			tags = arg[strings.Index(arg, "=")+1:]
		default:
			continue
		}
		for _, tag := range strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ' ' }) {
			if tag == "ktmldev" {
				return true
			}
		}
	}
	return false
}

func New(cmdArgs []string) *Runner {
	return &Runner{
		cmdArgs: cmdArgs,
	}
}

// NewBuild creates a build-then-run Runner. Every start first builds a fresh
// binary under binDir while the current process keeps serving, so a failed
// build or an unready replacement never costs the last working backend.
func NewBuild(buildArgs, cmdArgs []string, binDir string) *Runner {
	return &Runner{
		cmdArgs:   cmdArgs,
		buildArgs: buildArgs,
		binDir:    binDir,
	}
}

func (r *Runner) Start(ctx context.Context) error {
	if len(r.cmdArgs) == 0 {
		return errors.New("no command provided")
	}
	if len(r.buildArgs) == 0 {
		return r.start(ctx, "")
	}
	binary, err := r.build(ctx)
	if err != nil {
		return err
	}
	if err := r.start(ctx, binary); err != nil {
		_ = os.Remove(binary)
		return err
	}
	r.mu.Lock()
	r.binary = binary
	r.mu.Unlock()
	return nil
}

func (r *Runner) start(ctx context.Context, binary string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return errors.New("already running")
	}

	args := substituteBinary(r.cmdArgs, binary)
	r.cmd = exec.CommandContext(ctx, args[0], args[1:]...)
	r.cmd.Stdout = os.Stdout
	r.cmd.Stderr = os.Stderr
	if len(r.env) > 0 {
		r.cmd.Env = append(os.Environ(), r.env...)
	}
	r.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := r.cmd.Start(); err != nil {
		return err
	}

	r.running = true
	exited := make(chan struct{})
	r.exited = exited

	cmd := r.cmd
	go func() {
		_ = cmd.Wait()
		r.mu.Lock()
		if r.cmd == cmd {
			r.running = false
		}
		r.mu.Unlock()
		close(exited)
	}()

	return nil
}

func (r *Runner) build(ctx context.Context) (string, error) {
	if err := os.MkdirAll(r.binDir, 0o755); err != nil {
		return "", fmt.Errorf("create backend binary directory: %w", err)
	}
	r.mu.Lock()
	r.builds++
	binary, err := filepath.Abs(filepath.Join(r.binDir, fmt.Sprintf("backend-%d", r.builds)))
	r.mu.Unlock()
	if err != nil {
		return "", err
	}
	args := substituteBinary(r.buildArgs, binary)
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		_ = os.Remove(binary)
		return "", fmt.Errorf("build backend: %w", err)
	}
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("build backend did not produce %s: %w", binary, err)
	}
	return binary, nil
}

func (r *Runner) Stop() error {
	r.mu.Lock()
	if !r.running || r.cmd == nil || r.cmd.Process == nil {
		r.mu.Unlock()
		return nil
	}
	pid := r.cmd.Process.Pid
	exited := r.exited
	r.mu.Unlock()

	_ = syscall.Kill(-pid, syscall.SIGTERM)

	select {
	case <-exited:
		return nil
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-exited
		return nil
	}
}

// Restart replaces the running backend and returns once ready accepts the
// replacement. In build-then-run mode the replacement is built before the
// current process stops, and a replacement that exits or never becomes ready
// is stopped and the previous working binary is started again; the returned
// error still reports the failed reload. Without a build command the current
// process must stop before its replacement can be checked. ctx bounds the
// lifetime of the started processes, so ready must apply its own timeout.
func (r *Runner) Restart(ctx context.Context, ready ReadyFunc) error {
	if len(r.cmdArgs) == 0 {
		return errors.New("no command provided")
	}
	if len(r.buildArgs) == 0 {
		if _, err := exec.LookPath(r.cmdArgs[0]); err != nil {
			return err
		}
		slog.Warn("No dev build command configured; the running backend stops before its replacement is ready")
		if err := r.Stop(); err != nil {
			return err
		}
		if err := r.start(ctx, ""); err != nil {
			return err
		}
		return r.awaitReady(ctx, ready)
	}

	binary, err := r.build(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	previous := r.binary
	r.mu.Unlock()
	if err := r.Stop(); err != nil {
		_ = os.Remove(binary)
		return err
	}
	failure := r.start(ctx, binary)
	if failure == nil {
		failure = r.awaitReady(ctx, ready)
	}
	if failure == nil {
		r.mu.Lock()
		r.binary = binary
		r.mu.Unlock()
		if previous != "" {
			_ = os.Remove(previous)
		}
		return nil
	}
	failure = fmt.Errorf("replacement backend failed: %w", failure)
	_ = r.Stop()
	_ = os.Remove(binary)
	if previous == "" {
		return failure
	}
	if err := r.start(ctx, previous); err != nil {
		return errors.Join(failure, fmt.Errorf("restart previous backend: %w", err))
	}
	return failure
}

func (r *Runner) awaitReady(ctx context.Context, ready ReadyFunc) error {
	r.mu.Lock()
	exited := r.exited
	r.mu.Unlock()
	if ready == nil {
		return nil
	}
	readyCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- ready(readyCtx) }()
	select {
	case err := <-result:
		if err != nil {
			return err
		}
		select {
		case <-exited:
			return errors.New("backend exited before it became ready")
		default:
			return nil
		}
	case <-exited:
		return errors.New("backend exited before it became ready")
	}
}

func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Binary returns the build-then-run binary of the current working backend.
func (r *Runner) Binary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.binary
}

func substituteBinary(args []string, binary string) []string {
	substituted := make([]string, len(args))
	for i, arg := range args {
		if binary != "" {
			arg = strings.ReplaceAll(arg, BinaryPlaceholder, binary)
		}
		substituted[i] = arg
	}
	return substituted
}
