package buildstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHash(t *testing.T) {
	content := []byte("hello world")
	expectedBytes := sha256.Sum256(content)
	expected := hex.EncodeToString(expectedBytes[:])

	got := Hash(content)
	if got != expected {
		t.Errorf("Hash() = %v, want %v", got, expected)
	}
}

func TestLoad_NotExists(t *testing.T) {
	m, err := Load("non_existent_file.json")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("Load() returned nil manifest")
	}
	if m.Files == nil {
		t.Fatal("Load() returned manifest with nil Files map")
	}
}

func TestSave(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "buildstate-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "deep", "nested", "dir", "state.json")
	m := &Manifest{
		Files: map[string]FileEntry{
			"test.ktml": {
				Hash:             "hash123",
				PrimaryOutput:    "test.go",
				AuxiliaryOutputs: []string{"test.css"},
			},
		},
	}

	err = Save(path, m)
	if err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() unexpected error: %v", err)
	}

	var loaded Manifest
	err = json.Unmarshal(data, &loaded)
	if err != nil {
		t.Fatalf("json.Unmarshal() unexpected error: %v", err)
	}

	if loaded.Files["test.ktml"].Hash != "hash123" {
		t.Errorf("Save() did not persist correctly, got hash: %v", loaded.Files["test.ktml"].Hash)
	}
}
