package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/internal/config"
)

// devPlan is a resolved ktml dev run: the profile's dev configuration, the
// application's environment, and the capture request.
type devPlan struct {
	profile string
	root    string
	cfg     *config.Config
	dev     *config.Dev
	env     []config.EnvVar
	capture []string
	// envFileMissing marks a dry run whose env file does not exist.
	envFileMissing bool
}

// planDev applies the --profile and the --port, --proxy-target, and
// --explorer-addr overrides, reads its env file, and checks a capture
// request. lookup expands env file references to variables the file does
// not set.
func planDev(cfg *config.Config, root string, flags devFlags, lookup func(string) (string, bool)) (devPlan, error) {
	dev, err := cfg.DevProfile(flags.profile)
	if err != nil {
		return devPlan{}, err
	}
	dev, err = dev.WithOverrides(flags.overrides)
	if err != nil {
		return devPlan{}, err
	}
	resolved := *cfg
	resolved.Dev = dev
	env, err := dev.Environment(root, lookup)
	missing := false
	if errors.Is(err, fs.ErrNotExist) && flags.dryRun {
		// A dry run still shows the rest of the profile.
		withoutFile := *dev
		withoutFile.EnvFile = ""
		env, err = withoutFile.Environment(root, lookup)
		missing = true
	}
	if err != nil {
		return devPlan{}, err
	}
	capture, err := devCaptureEnv(&resolved, root, flags)
	if err != nil {
		return devPlan{}, err
	}
	return devPlan{profile: flags.profile, root: root, cfg: &resolved, dev: dev, env: env, capture: capture, envFileMissing: missing}, nil
}

// processEnv is the application's environment over ktml dev's own: the env
// file and dev.env, then the control channel and capture variables, which
// nothing else may set.
func (p devPlan) processEnv(token, workspaceRoot string) []string {
	env := make([]string, 0, len(p.env)+2+len(p.capture))
	for _, variable := range p.env {
		env = append(env, variable.Key+"="+variable.Value)
	}
	env = append(env, hmr.TokenEnv+"="+token, hmr.RootEnv+"="+workspaceRoot)
	return append(env, p.capture...)
}

// liveWarning is printed whenever a profile loads an env file: it may point
// the application at production services.
const liveWarning = "WARNING: this run loads %s into the application. If it points at production services, everything you do in the browser (forms, buttons, HTMX requests) changes production data. The proxy, Explorer, and control channel stay on loopback; story capture stays off unless the app declares local data.\n"

// writeDevPlan prints a dry run: what ktml dev would build, run, and serve,
// and the names, never the values, of the application's variables.
func writeDevPlan(w io.Writer, p devPlan) {
	profile := p.profile
	if profile == "" {
		profile = "(default)"
	}
	dev := p.dev
	fmt.Fprintln(w, "ktml dev --dry-run: nothing is built, started, or connected")
	fmt.Fprintf(w, "  profile:       %s\n", profile)
	fmt.Fprintf(w, "  proxy:         http://%s -> %s\n", devProxyAddr(dev.Port), dev.ProxyTarget)
	if len(dev.Build) > 0 {
		fmt.Fprintf(w, "  build:         %s\n", strings.Join(dev.Build, " "))
	}
	fmt.Fprintf(w, "  command:       %s\n", strings.Join(dev.Command, " "))
	if dev.Explorer != nil {
		fmt.Fprintf(w, "  explorer:      http://%s/\n", dev.Explorer.Addr)
	} else {
		fmt.Fprintln(w, "  explorer:      off")
	}
	if dev.PickerEnabled() {
		fmt.Fprintf(w, "  picker:        %s\n", dev.PickerHotkey())
		if editor := dev.PickerEditor(); editor != nil {
			fmt.Fprintf(w, "  editor:        %s\n", strings.Join(editor, " "))
		}
	} else {
		fmt.Fprintln(w, "  picker:        off")
	}
	ready := time.Duration(dev.ReadyTimeoutMS) * time.Millisecond
	if ready == 0 {
		ready = 5 * time.Second
	}
	fmt.Fprintf(w, "  ready timeout: %s\n", ready)
	if dev.EnvFile != "" {
		path := dev.EnvFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.root, filepath.FromSlash(path))
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		if p.envFileMissing {
			fmt.Fprintf(w, "  env file:      %s (missing: ktml dev --profile %s would refuse to start)\n", path, p.profile)
		} else {
			fmt.Fprintf(w, "  env file:      %s\n", path)
		}
	} else {
		fmt.Fprintln(w, "  env file:      none")
	}
	if len(p.env) > 0 {
		names := make([]string, 0, len(p.env))
		for _, variable := range p.env {
			names = append(names, fmt.Sprintf("%s (%s)", variable.Key, variable.Source))
		}
		fmt.Fprintf(w, "  app env:       %d variables, values elided:\n", len(p.env))
		for _, name := range names {
			fmt.Fprintf(w, "                   %s\n", name)
		}
	}
	fmt.Fprintf(w, "  control env:   %s (a new token each run), %s\n", hmr.TokenEnv, hmr.RootEnv)
	if len(p.capture) > 0 {
		fmt.Fprintf(w, "  capture:       %s\n", strings.Join(p.capture, " "))
	} else {
		fmt.Fprintln(w, "  capture:       off")
	}
	if dev.EnvFile != "" {
		fmt.Fprintf(w, liveWarning, dev.EnvFile)
	}
}
