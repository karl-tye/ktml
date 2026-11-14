package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func runGenerate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	err := generateCmd(t.Context(), args, &stdout)
	return stdout.String(), err
}

func TestGenerateCmd_GIVENNoVerboseFlag_WHENGenerating_THENLogsNoDebugRecords(t *testing.T) {
	for _, tt := range []struct {
		args  []string
		debug bool
	}{
		{args: []string{"ui"}},
		{args: []string{"-v", "ui"}, debug: true},
		{args: []string{"ui", "-verbose"}, debug: true},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			writeComponentProject(t, map[string]string{"ui/card.ktml": componentProject["ui/card.ktml"]})
			var logs bytes.Buffer
			previous, previousLevel := slog.Default(), logLevel.Level()
			t.Cleanup(func() { slog.SetDefault(previous); logLevel.Set(previousLevel) })
			logLevel.Set(defaultLogLevel())
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: logLevel})))
			if _, err := runGenerate(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(logs.String(), "level=DEBUG"); got != tt.debug {
				t.Errorf("debug records logged = %v, want %v:\n%s", got, tt.debug, logs.String())
			}
		})
	}
}

func TestGenerateCmd_GIVENUnknownFlagOrNoPaths_WHENRun_THENReturnsUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"-v"}, {"-check"}, {"-bogus", "ui"}} {
		if _, err := runGenerate(t, args...); err == nil || !strings.Contains(err.Error(), "usage: ktml generate") {
			t.Errorf("generate %v error = %v, want usage", args, err)
		}
	}
}
