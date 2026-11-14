package main

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/internal/config"
)

// TestDevCaptureEnv_GIVENACaptureRequest_WHENChecked_THENNeedsTheProjectsConsent
// refuses --capture-stories without dev.capture.allow, for a remote app,
// and outside the project, and otherwise asks the app to capture.
func TestDevCaptureEnv_GIVENACaptureRequest_WHENChecked_THENNeedsTheProjectsConsent(t *testing.T) {
	root := t.TempDir()
	local := &config.Dev{ProxyTarget: "http://127.0.0.1:8080"}
	allowed := &config.Dev{ProxyTarget: "http://127.0.0.1:8080", Capture: &config.DevCapture{Allow: true}}
	remote := &config.Dev{ProxyTarget: "https://app.example.com", AllowRemoteProxyTarget: true, Capture: &config.DevCapture{Allow: true}}
	tests := []struct {
		name  string
		dev   *config.Dev
		flags devFlags
		env   []string
		error string
	}{
		{"off", local, devFlags{}, nil, ""},
		{"no consent", local, devFlags{captureDir: "captured"}, nil, `"allow": true`},
		{"consent false", &config.Dev{ProxyTarget: "http://127.0.0.1:8080", Capture: &config.DevCapture{}}, devFlags{captureDir: "captured"}, nil, `"allow": true`},
		{"remote app", remote, devFlags{captureDir: "captured"}, nil, "loopback"},
		{"outside the project", allowed, devFlags{captureDir: "../elsewhere"}, nil, "outside the workspace"},
		{"inside .git", allowed, devFlags{captureDir: ".git/stories"}, nil, ".git"},
		{"redacted", allowed, devFlags{captureDir: "captured"}, []string{hmr.CaptureEnv + "=captured"}, ""},
		{"raw", allowed, devFlags{captureDir: "captured", captureRaw: true}, []string{hmr.CaptureEnv + "=captured", hmr.CaptureRawEnv + "=1"}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env, err := devCaptureEnv(&config.Config{Dev: test.dev}, root, test.flags)
			if test.error != "" {
				if err == nil || !strings.Contains(err.Error(), test.error) {
					t.Fatalf("error = %v, want %q", err, test.error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(env, test.env) {
				t.Errorf("env = %v, want %v", env, test.env)
			}
		})
	}
}

// TestParseDevFlags_GIVENRawWithoutADirectory_WHENParsed_THENFails rejects
// --capture-raw alone and stray arguments.
func TestParseDevFlags_GIVENRawWithoutADirectory_WHENParsed_THENFails(t *testing.T) {
	if _, err := parseDevFlags([]string{"--capture-raw"}, io.Discard); err == nil {
		t.Error("--capture-raw alone was accepted")
	}
	if _, err := parseDevFlags([]string{"extra"}, io.Discard); err == nil {
		t.Error("a stray argument was accepted")
	}
	flags, err := parseDevFlags([]string{"--capture-stories", "captured", "--capture-raw"}, io.Discard)
	if err != nil || flags.captureDir != "captured" || !flags.captureRaw {
		t.Errorf("flags = %+v, %v", flags, err)
	}
}
