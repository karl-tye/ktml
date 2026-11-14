package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/dev/control"
	"github.com/karl-tye/ktml/internal/storygen"
)

// devFlags are the command-line options of ktml dev.
type devFlags struct {
	captureDir string
	captureRaw bool
	profile    string
	dryRun     bool
}

func parseDevFlags(args []string, stderr io.Writer) (devFlags, error) {
	var flags devFlags
	set := flag.NewFlagSet("dev", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&flags.captureDir, "capture-stories", "", "capture a story for each new shape of props the app renders, into `dir` inside the project (needs dev.capture.allow)")
	set.BoolVar(&flags.captureRaw, "capture-raw", false, "keep raw values in captured stories instead of redacting them; for local use only")
	set.StringVar(&flags.profile, "profile", "", "apply dev.profiles.`name` from ktml.config.json")
	set.BoolVar(&flags.dryRun, "dry-run", false, "print the resolved command, ports, and env file (variable names only) and exit without starting anything")
	if err := set.Parse(args); err != nil {
		return flags, err
	}
	if set.NArg() > 0 {
		fmt.Fprintf(stderr, "ktml dev takes no arguments, got %q\n", set.Args())
		return flags, errors.New("unexpected arguments")
	}
	if flags.captureRaw && flags.captureDir == "" {
		fmt.Fprintln(stderr, "--capture-raw needs --capture-stories <dir>")
		return flags, errors.New("--capture-raw without --capture-stories")
	}
	return flags, nil
}

// devCaptureEnv checks a capture request against the project's consent and
// returns the environment that asks the application to capture. The
// application itself must also declare local data (hmr.LocalData), so
// capture needs both the project's and the application's consent.
func devCaptureEnv(cfg *config.Config, root string, flags devFlags) ([]string, error) {
	if flags.captureDir == "" {
		return nil, nil
	}
	if !cfg.Dev.CaptureAllowed() {
		return nil, errors.New(`--capture-stories needs "dev": {"capture": {"allow": true}} in ktml.config.json; set it only in a project whose development app reads local data, since captured page values are written into story files`)
	}
	if cfg.Dev.RemoteProxyTarget() {
		return nil, errors.New("--capture-stories captures only from an app on a loopback host; dev.proxy_target is remote")
	}
	// The application checks the directory again; refusing here fails
	// before anything starts.
	if _, err := storygen.NewCapturer(root, flags.captureDir, nil); err != nil {
		return nil, err
	}
	env := []string{hmr.CaptureEnv + "=" + flags.captureDir}
	if flags.captureRaw {
		env = append(env, hmr.CaptureRawEnv+"=1")
	}
	return env, nil
}

// reportCapture prints the application's capture status once it answers.
func reportCapture(ctx context.Context, stderr io.Writer, client *control.Client, wait time.Duration) {
	if wait <= 0 {
		wait = 5 * time.Second
	}
	deadline := time.Now().Add(wait + 5*time.Second)
	for time.Now().Before(deadline) {
		attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
		status, err := client.Capture(attempt)
		cancel()
		if err == nil {
			switch status.Status {
			case hmr.CaptureActive:
				mode := "redacted"
				if status.Raw {
					mode = "raw values"
				}
				fmt.Fprintf(stderr, "KTML story capture: capturing into %s (%s)\n", status.Dir, mode)
			case hmr.CaptureRefused:
				fmt.Fprintf(stderr, "KTML story capture: refused by the application: %s\n", status.Reason)
			default:
				fmt.Fprintln(stderr, "KTML story capture: off in the application; build it with -tags ktmldev and hmr.Install (ADR-0004)")
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	fmt.Fprintln(stderr, "KTML story capture: the application did not answer the control channel; is it built with -tags ktmldev and hmr.Install?")
}
