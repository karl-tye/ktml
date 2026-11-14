//go:build js || plan9

package buildstate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPublish_GIVENPlatformCannotProvideRootSafety_WHENPublishing_THENFailsClosed(t *testing.T) {
	project := t.TempDir()
	err := Publish(filepath.Join(project, ".ktml", "state.json"), &Manifest{Files: make(map[string]FileEntry)}, "page.ktml", FileEntry{
		Hash:          "hash",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})

	if err == nil || !strings.Contains(err.Error(), "secure rooted filesystem operations are unsupported") {
		t.Fatalf("Publish() error = %v, want unsupported-platform rejection", err)
	}
}
