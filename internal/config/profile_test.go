package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/configdiag"
)

const profiledDev = `{"dev": {
	"proxy_target": "http://127.0.0.1:8080",
	"build": ["go", "build", "-tags", "ktmldev", "-o", "{binary}", "./cmd/server"],
	"command": ["{binary}", "-local", "-port", "8080"],
	"watch": ["*.ktml"],
	"port": 7330,
	"env": {"LOG_LEVEL": "debug", "REGION": "local"},
	"explorer": {"addr": "127.0.0.1:7339"},
	"profiles": {
		"live": {
			"proxy_target": "http://127.0.0.1:8090",
			"command": ["{binary}", "-port", "8090"],
			"port": 7340,
			"env_file": ".env",
			"env": {"REGION": "eu"},
			"explorer": {"addr": "127.0.0.1:7349"},
			"ready_timeout_ms": 60000
		}
	}
}}`

func TestDevProfile_GIVENAProfile_WHENResolved_THENOverridesOnlyItsKeysAndMergesEnv(t *testing.T) {
	cfg, err := Parse([]byte(profiledDev))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	live, err := cfg.DevProfile("live")
	if err != nil {
		t.Fatalf("DevProfile(live) error = %v", err)
	}
	if live.ProxyTarget != "http://127.0.0.1:8090" || live.Port != 7340 || live.Explorer.Addr != "127.0.0.1:7349" || live.ReadyTimeoutMS != 60000 || live.EnvFile != ".env" {
		t.Errorf("live = %+v, want the profile's proxy target, ports, env file and ready timeout", live)
	}
	if !reflect.DeepEqual(live.Command, []string{"{binary}", "-port", "8090"}) {
		t.Errorf("live command = %v", live.Command)
	}
	// Keys the profile leaves out come from dev.
	if !reflect.DeepEqual(live.Build, cfg.Dev.Build) || !reflect.DeepEqual(live.Watch, []string{"*.ktml"}) {
		t.Errorf("live build/watch = %v %v, want dev's", live.Build, live.Watch)
	}
	if want := map[string]string{"LOG_LEVEL": "debug", "REGION": "eu"}; !reflect.DeepEqual(live.Env, want) {
		t.Errorf("live env = %v, want %v", live.Env, want)
	}
	if live.Profiles != nil {
		t.Errorf("a resolved profile has no profiles of its own: %v", live.Profiles)
	}
	// The base dev is unchanged and is the default profile.
	base, err := cfg.DevProfile("")
	if err != nil || base != cfg.Dev || base.Port != 7330 || base.Env["REGION"] != "local" {
		t.Errorf("DevProfile(\"\") = %+v, %v; want dev unchanged", base, err)
	}
	if _, err := cfg.DevProfile("staging"); err == nil || !strings.Contains(err.Error(), "live") {
		t.Errorf("unknown profile error = %v, want one naming the defined profiles", err)
	}
}

func TestDevProfile_GIVENInvalidProfiles_WHENParsed_THENRejectsAtTheProfileKey(t *testing.T) {
	tests := []struct {
		name, profile, path string
	}{
		{"remote target", `"proxy_target": "http://10.0.0.5:8090"`, "dev.profiles.live.proxy_target"},
		{"non-loopback explorer", `"explorer": {"addr": "0.0.0.0:7349"}`, "dev.profiles.live.explorer.addr"},
		{"negative timeout", `"ready_timeout_ms": -5`, "dev.profiles.live.ready_timeout_ms"},
		{"command without binary", `"command": ["./server"]`, "dev.profiles.live"},
		{"key a profile cannot set", `"capture": {"allow": true}`, "dev.profiles.live.capture"},
		{"remote opt-in", `"allow_remote_proxy_target": true`, "dev.profiles.live.allow_remote_proxy_target"},
		{"control variable", `"env": {"KTML_DEV_TOKEN": "x"}`, "dev.profiles.live.env.KTML_DEV_TOKEN"},
		{"bad variable name", `"env": {"1BAD": "x"}`, "dev.profiles.live.env.1BAD"},
		{"empty env file", `"env_file": ""`, "dev.profiles.live.env_file"},
	}
	for _, tt := range tests {
		source := `{"dev": {"proxy_target": "http://127.0.0.1:8080", "build": ["go", "build", "-o", "{binary}"], "command": ["{binary}"], "profiles": {"live": {` + tt.profile + `}}}}`
		_, err := Parse([]byte(source))
		var pathError *configdiag.PathError
		if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != tt.path {
			t.Errorf("%s: error = %v, want a path error at %s", tt.name, err, tt.path)
		}
	}
	_, err := Parse([]byte(`{"dev": {"proxy_target": "http://127.0.0.1:8080", "command": ["x"], "env": {"KTML_DEV_CAPTURE": "stories"}}}`))
	var pathError *configdiag.PathError
	if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != "dev.env.KTML_DEV_CAPTURE" {
		t.Errorf("dev.env control variable error = %v, want a path error at dev.env.KTML_DEV_CAPTURE", err)
	}
}

func TestDevEnvironment_GIVENAnEnvFileAndEnv_WHENLoaded_THENEnvOverridesTheFileInOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("# secrets\nDATABASE_URL=postgres://user:pw@db/app\nREGION=us\nexport TOKEN='s3cr3t'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse([]byte(profiledDev))
	if err != nil {
		t.Fatal(err)
	}
	live, _ := cfg.DevProfile("live")
	env, err := live.Environment(root, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Environment() error = %v", err)
	}
	want := []EnvVar{
		{Key: "DATABASE_URL", Value: "postgres://user:pw@db/app", Source: ".env:2"},
		{Key: "TOKEN", Value: "s3cr3t", Source: ".env:4"},
		{Key: "LOG_LEVEL", Value: "debug", Source: "dev.env"},
		{Key: "REGION", Value: "eu", Source: "dev.env"},
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("Environment() = %+v\nwant %+v", env, want)
	}
	if _, err := cfg.Dev.Environment(root, nil); err != nil {
		t.Errorf("dev without an env file: %v", err)
	}
	if err := os.Remove(filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Environment(root, nil); err == nil || !strings.Contains(err.Error(), ".env") {
		t.Errorf("missing env file error = %v, want one naming the file", err)
	}
}

func TestDevEnvironment_GIVENAnEnvFileSettingAControlVariable_WHENLoaded_THENRefusesIt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("KTML_DEV_CAPTURE=stories\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dev := &Dev{EnvFile: ".env"}
	if _, err := dev.Environment(root, nil); err == nil || !strings.Contains(err.Error(), "KTML_DEV_CAPTURE") {
		t.Errorf("error = %v, want a refusal naming KTML_DEV_CAPTURE", err)
	}
}

func TestDevPicker_GIVENHotkeys_WHENParsed_THENCanonicalizesOrRejects(t *testing.T) {
	parse := func(picker string) (*Dev, error) {
		cfg, err := Parse([]byte(`{"dev": {"proxy_target": "http://127.0.0.1:8080", "command": ["x"], "explorer": {}` + picker + `}}`))
		if err != nil {
			return nil, err
		}
		return cfg.Dev, nil
	}
	dev, err := parse("")
	if err != nil || !dev.PickerEnabled() || dev.PickerHotkey() != DefaultPickerHotkey {
		t.Fatalf("default picker = %v, %v; want enabled with %s", dev, err, DefaultPickerHotkey)
	}
	dev, err = parse(`, "picker": {"hotkey": "shift+ctrl+p"}`)
	if err != nil || dev.PickerHotkey() != "Ctrl+Shift+P" {
		t.Errorf("hotkey = %v, %v; want Ctrl+Shift+P", dev, err)
	}
	dev, err = parse(`, "picker": {"disabled": true}`)
	if err != nil || dev.PickerEnabled() {
		t.Errorf("disabled picker = %v, %v", dev, err)
	}
	for _, hotkey := range []string{"K", "Shift+K", "Alt+Shift", "Alt+Enter", "Alt+Hyper+K", ""} {
		_, err := parse(`, "picker": {"hotkey": "` + hotkey + `"}`)
		var pathError *configdiag.PathError
		if hotkey == "" {
			if err != nil {
				t.Errorf("an empty hotkey takes the default: %v", err)
			}
			continue
		}
		if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != "dev.picker.hotkey" {
			t.Errorf("hotkey %q: error = %v, want a path error at dev.picker.hotkey", hotkey, err)
		}
	}
	cfg, err := Parse([]byte(`{"dev": {"proxy_target": "http://127.0.0.1:8080", "command": ["x"]}}`))
	if err != nil || cfg.Dev.PickerEnabled() {
		t.Errorf("without the Explorer the picker is off: %v %v", cfg, err)
	}
}
