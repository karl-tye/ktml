package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/config"
)

const liveProfileConfig = `{"dev": {
	"proxy_target": "http://localhost:8080",
	"build": ["go", "build", "-tags", "ktmldev", "-o", "{binary}", "./cmd/server"],
	"command": ["{binary}", "-local", "-port", "8080"],
	"port": 7330,
	"explorer": {"addr": "127.0.0.1:7339"},
	"profiles": {"live": {
		"proxy_target": "http://localhost:8090",
		"command": ["{binary}", "-port", "8090"],
		"port": 7340,
		"env_file": ".env",
		"env": {"APP_MODE": "live"},
		"explorer": {"addr": "127.0.0.1:7349"}
	}}
}}`

// TestDevDryRun_GIVENTheLiveProfile_WHENPrinted_THENShowsTheResolvedRunWithoutSecretValues
// resolves a profile and prints its command, ports, env file, and variable
// names, never a value, and starts nothing.
func TestDevDryRun_GIVENTheLiveProfile_WHENPrinted_THENShowsTheResolvedRunWithoutSecretValues(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_URL=postgres://admin:hunter2@prod-db/shop\nexport STRIPE_KEY='sk_live_secret'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse([]byte(liveProfileConfig))
	if err != nil {
		t.Fatal(err)
	}
	flags, err := parseDevFlags([]string{"--profile", "live", "--dry-run"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planDev(cfg, root, flags, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("planDev() error = %v", err)
	}
	var out bytes.Buffer
	writeDevPlan(&out, plan)
	printed := out.String()
	for _, want := range []string{
		"profile:       live",
		"proxy:         http://127.0.0.1:7340 -> http://localhost:8090",
		"build:         go build -tags ktmldev -o {binary} ./cmd/server",
		"command:       {binary} -port 8090",
		"explorer:      http://127.0.0.1:7349/",
		"picker:        Alt+Shift+K",
		filepath.Join(root, ".env"),
		"DATABASE_URL (.env:1)",
		"STRIPE_KEY (.env:2)",
		"APP_MODE (dev.env)",
		"values elided",
		"WARNING",
		"production",
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("dry run does not show %q:\n%s", want, printed)
		}
	}
	for _, secret := range []string{"hunter2", "sk_live_secret", "prod-db", "=live"} {
		if strings.Contains(printed, secret) {
			t.Errorf("dry run prints the value %q:\n%s", secret, printed)
		}
	}
	// The application's environment carries the values, under ktml dev's
	// own control variables.
	env := plan.processEnv("token", "/root")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "DATABASE_URL=postgres://admin:hunter2@prod-db/shop") || !strings.HasSuffix(joined, "KTML_DEV_TOKEN=token\nKTML_DEV_ROOT=/root") {
		t.Errorf("process env = %v", env)
	}

	// The default profile loads no env file and has no warning.
	plan, err = planDev(cfg, root, devFlags{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	writeDevPlan(&out, plan)
	if strings.Contains(out.String(), "WARNING") || !strings.Contains(out.String(), "env file:      none") || !strings.Contains(out.String(), "proxy:         http://127.0.0.1:7330 -> http://localhost:8080") {
		t.Errorf("default plan:\n%s", out.String())
	}
	if _, err := planDev(cfg, root, devFlags{profile: "staging"}, nil); err == nil || !strings.Contains(err.Error(), "live") {
		t.Errorf("unknown profile error = %v", err)
	}
}

// TestDevPlan_GIVENTheLiveProfileAndCapture_WHENPlanned_THENCaptureIsRefusedWithoutConsent
// keeps story capture off a profile whose project has not allowed it.
func TestDevPlan_GIVENTheLiveProfileAndCapture_WHENPlanned_THENCaptureIsRefusedWithoutConsent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse([]byte(liveProfileConfig))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planDev(cfg, root, devFlags{profile: "live", captureDir: "captured"}, nil); err == nil || !strings.Contains(err.Error(), `"allow": true`) {
		t.Errorf("capture on live: error = %v, want the consent refusal", err)
	}
}

// TestParseDevFlags_GIVENProfileFlags_WHENParsed_THENKeepsThem parses
// --profile and --dry-run.
func TestParseDevFlags_GIVENProfileFlags_WHENParsed_THENKeepsThem(t *testing.T) {
	flags, err := parseDevFlags([]string{"--profile=live", "--dry-run"}, io.Discard)
	if err != nil || flags.profile != "live" || !flags.dryRun {
		t.Errorf("flags = %+v, %v", flags, err)
	}
}

// TestDevDryRun_GIVENAMissingEnvFile_WHENPrinted_THENReportsItAndARealRunRefuses
// lets a dry run show a profile whose env file is not there yet, while the
// real run fails before starting anything.
func TestDevDryRun_GIVENAMissingEnvFile_WHENPrinted_THENReportsItAndARealRunRefuses(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Parse([]byte(liveProfileConfig))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planDev(cfg, root, devFlags{profile: "live", dryRun: true}, nil)
	if err != nil {
		t.Fatalf("dry run with a missing env file: %v", err)
	}
	var out bytes.Buffer
	writeDevPlan(&out, plan)
	if !strings.Contains(out.String(), "(missing: ktml dev --profile live would refuse to start)") || !strings.Contains(out.String(), "APP_MODE (dev.env)") {
		t.Errorf("dry run:\n%s", out.String())
	}
	if _, err := planDev(cfg, root, devFlags{profile: "live"}, nil); err == nil || !strings.Contains(err.Error(), ".env") {
		t.Errorf("real run with a missing env file: %v", err)
	}
}
