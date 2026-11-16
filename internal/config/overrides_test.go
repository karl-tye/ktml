package config

import (
	"reflect"
	"strings"
	"testing"
)

const overrideBase = `{"dev": {
	"proxy_target": "http://127.0.0.1:3000",
	"build": ["go", "build", "-o", "{binary}", "./cmd/shop"],
	"command": ["{binary}", "-addr", "127.0.0.1:{target_port}"],
	"env": {"SHOP_PORT": "{target_port}", "SHOP_MODE": "dev"},
	"port": 4100,
	"explorer": {"addr": "127.0.0.1:4200"}
}}`

func parseOverrideBase(t *testing.T, source string) *Dev {
	t.Helper()
	cfg, err := Parse([]byte(source))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return cfg.Dev
}

func TestDevWithOverrides_GIVENFlags_WHENApplied_THENReplacesPortsAndTargetWithoutChangingTheOriginal(t *testing.T) {
	dev := parseOverrideBase(t, overrideBase)

	got, err := dev.WithOverrides(DevOverrides{Port: 4101, ProxyTarget: "http://localhost:3001", ExplorerAddr: "[::1]:4201"})
	if err != nil {
		t.Fatalf("WithOverrides() error = %v", err)
	}

	if got.Port != 4101 || got.ProxyTarget != "http://localhost:3001" || got.Explorer.Addr != "[::1]:4201" {
		t.Errorf("overridden dev = port %d, target %q, explorer %q", got.Port, got.ProxyTarget, got.Explorer.Addr)
	}
	if dev.Port != 4100 || dev.ProxyTarget != "http://127.0.0.1:3000" || dev.Explorer.Addr != "127.0.0.1:4200" {
		t.Errorf("WithOverrides changed the original dev: %+v", dev)
	}
}

func TestDevWithOverrides_GIVENNoFlags_WHENApplied_THENKeepsTheConfiguration(t *testing.T) {
	dev := parseOverrideBase(t, overrideBase)

	got, err := dev.WithOverrides(DevOverrides{})
	if err != nil {
		t.Fatalf("WithOverrides() error = %v", err)
	}

	if got.Port != 4100 || got.ProxyTarget != "http://127.0.0.1:3000" || got.Explorer.Addr != "127.0.0.1:4200" {
		t.Errorf("dev without overrides = %+v", got)
	}
}

func TestDevWithOverrides_GIVENExplorerAddrWithoutDevExplorer_WHENApplied_THENServesTheExplorerThere(t *testing.T) {
	dev := parseOverrideBase(t, `{"dev": {"proxy_target": "http://127.0.0.1:3000", "command": ["shop"]}}`)

	got, err := dev.WithOverrides(DevOverrides{ExplorerAddr: "localhost:0"})
	if err != nil {
		t.Fatalf("WithOverrides() error = %v", err)
	}

	if got.Explorer == nil || got.Explorer.Addr != "localhost:0" {
		t.Errorf("explorer = %+v, want one on localhost:0", got.Explorer)
	}
}

func TestDevWithOverrides_GIVENInvalidFlag_WHENApplied_THENFailsNamingTheFlag(t *testing.T) {
	for _, tt := range []struct {
		name      string
		overrides DevOverrides
		want      string
	}{
		{"port too large", DevOverrides{Port: 70000}, "--port"},
		{"negative port", DevOverrides{Port: -1}, "--port"},
		{"remote target", DevOverrides{ProxyTarget: "http://192.0.2.10:3000"}, "--proxy-target"},
		{"target without scheme", DevOverrides{ProxyTarget: "localhost:3000"}, "--proxy-target"},
		{"target with credentials", DevOverrides{ProxyTarget: "http://user:pass@localhost:3000"}, "--proxy-target"},
		{"remote explorer", DevOverrides{ExplorerAddr: "0.0.0.0:4200"}, "--explorer-addr"},
		{"explorer without port", DevOverrides{ExplorerAddr: "127.0.0.1"}, "--explorer-addr"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dev := parseOverrideBase(t, overrideBase)

			_, err := dev.WithOverrides(tt.overrides)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("WithOverrides(%+v) error = %v, want one naming %s", tt.overrides, err, tt.want)
			}
		})
	}
}

func TestDevWithOverrides_GIVENTargetPortPlaceholder_WHENApplied_THENFollowsTheProxyTargetPort(t *testing.T) {
	dev := parseOverrideBase(t, overrideBase)

	got, err := dev.WithOverrides(DevOverrides{ProxyTarget: "http://127.0.0.1:3005"})
	if err != nil {
		t.Fatalf("WithOverrides() error = %v", err)
	}

	if want := []string{"{binary}", "-addr", "127.0.0.1:3005"}; !reflect.DeepEqual(got.Command, want) {
		t.Errorf("command = %q, want %q", got.Command, want)
	}
	if want := []string{"go", "build", "-o", "{binary}", "./cmd/shop"}; !reflect.DeepEqual(got.Build, want) {
		t.Errorf("build = %q, want %q", got.Build, want)
	}
	if got.Env["SHOP_PORT"] != "3005" || got.Env["SHOP_MODE"] != "dev" {
		t.Errorf("env = %v, want SHOP_PORT to follow the target", got.Env)
	}
	if dev.Command[2] != "127.0.0.1:{target_port}" || dev.Env["SHOP_PORT"] != "{target_port}" {
		t.Errorf("substitution changed the original dev: %q %v", dev.Command, dev.Env)
	}
}

func TestDevWithOverrides_GIVENTargetWithoutPort_WHENSubstituted_THENUsesTheSchemeDefault(t *testing.T) {
	dev := parseOverrideBase(t, `{"dev": {"proxy_target": "http://localhost", "command": ["shop", "-port", "{target_port}"]}}`)

	got, err := dev.WithOverrides(DevOverrides{})
	if err != nil {
		t.Fatalf("WithOverrides() error = %v", err)
	}

	if got.Command[2] != "80" {
		t.Errorf("command = %q, want the http default port", got.Command)
	}
}

func TestParse_GIVENOutOfRangeDevPort_WHENParsed_THENReportsDevPort(t *testing.T) {
	_, err := Parse([]byte(`{"dev": {"proxy_target": "http://127.0.0.1:3000", "command": ["shop"], "port": 70000}}`))

	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("Parse() error = %v, want a dev.port error", err)
	}
}
