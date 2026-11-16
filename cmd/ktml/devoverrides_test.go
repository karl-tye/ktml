package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/config"
)

const overrideShopConfig = `{"dev": {
	"proxy_target": "http://127.0.0.1:3000",
	"command": ["go", "run", "./cmd/shop", "-addr", "127.0.0.1:{target_port}"],
	"port": 4100,
	"explorer": {"addr": "127.0.0.1:4200"},
	"profiles": {"alt": {"port": 4300}}
}}`

// TestDevDryRun_GIVENPortFlags_WHENPlanned_THENFlagsOverrideTheProfile lets a
// Makefile's port variables drive ktml dev.
func TestDevDryRun_GIVENPortFlags_WHENPlanned_THENFlagsOverrideTheProfile(t *testing.T) {
	cfg, err := config.Parse([]byte(overrideShopConfig))
	if err != nil {
		t.Fatal(err)
	}
	flags, err := parseDevFlags([]string{"--profile", "alt", "--port", "4101", "--proxy-target", "http://localhost:3001", "--explorer-addr", "127.0.0.1:4201", "--dry-run"}, io.Discard)
	if err != nil {
		t.Fatalf("parseDevFlags() error = %v", err)
	}

	plan, err := planDev(cfg, t.TempDir(), flags, nil)
	if err != nil {
		t.Fatalf("planDev() error = %v", err)
	}

	var out bytes.Buffer
	writeDevPlan(&out, plan)
	for _, want := range []string{
		"profile:       alt",
		"proxy:         http://127.0.0.1:4101 -> http://localhost:3001",
		"command:       go run ./cmd/shop -addr 127.0.0.1:3001",
		"explorer:      http://127.0.0.1:4201/",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run does not show %q:\n%s", want, out.String())
		}
	}
	if plan.cfg.Dev != plan.dev || plan.dev.Port != 4101 {
		t.Errorf("plan dev = %+v, want the overridden dev in the run configuration", plan.dev)
	}
}

// TestDevPlan_GIVENANonLoopbackFlag_WHENPlanned_THENRefusesNamingTheFlag
// validates flags as the configuration is: loopback only.
func TestDevPlan_GIVENANonLoopbackFlag_WHENPlanned_THENRefusesNamingTheFlag(t *testing.T) {
	cfg, err := config.Parse([]byte(overrideShopConfig))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--proxy-target", "http://192.0.2.10:3000"},
		{"--explorer-addr", "0.0.0.0:4200"},
		{"--port", "0"},
		{"--port", "65536"},
	} {
		flags, err := parseDevFlags(args, io.Discard)
		if err == nil {
			_, err = planDev(cfg, t.TempDir(), flags, nil)
		}
		if err == nil || !strings.Contains(err.Error(), args[0]) {
			t.Errorf("%v: error = %v, want one naming %s", args, err, args[0])
		}
	}
}
