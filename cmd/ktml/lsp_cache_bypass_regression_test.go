package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGenerateFile_GIVENConfiguredLSPProxy_WHENGeneratingUnchangedSourceTwice_THENBypassesGenerationCache(t *testing.T) {
	t.Setenv("KTML_CLI_CACHE_HELPER", "1")
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	statePath := filepath.Join(project, "lsp-calls")
	pluginPath := buildCLIProxyOutputPlugin(t)
	commandJSON := quoteJSON(t, os.Args[0]) + `,"-test.run=^TestCLICacheLSPHelperProcess$","--",` + quoteJSON(t, statePath)
	configJSON := `{"package_dir":".","lsp":{"go":{"command":[` + commandJSON + `]}},"resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
	if err := os.WriteFile("ktml.config.json", []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	for range 2 {
		if err := generateFile("page.ktml", false); err != nil {
			t.Fatalf("generateFile() error = %v", err)
		}
	}
	if got := string(mustReadFile(t, "page.out")); got != "generation-2" {
		t.Fatalf("generated output = %q, want second LSP-backed generation", got)
	}
	if got := string(mustReadFile(t, statePath)); got != "2" {
		t.Fatalf("LSP helper calls = %q, want 2", got)
	}
}

func TestCLICacheLSPHelperProcess(t *testing.T) {
	if os.Getenv("KTML_CLI_CACHE_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == 0 || len(os.Args) <= separator+1 {
		os.Exit(2)
	}
	statePath := os.Args[separator+1]
	call := 1
	if contents, err := os.ReadFile(statePath); err == nil {
		previous, parseErr := strconv.Atoi(string(contents))
		if parseErr != nil {
			os.Exit(3)
		}
		call = previous + 1
	}
	if err := os.WriteFile(statePath, []byte(strconv.Itoa(call)), 0o600); err != nil {
		os.Exit(4)
	}
	payload, err := readCLILSPFrame(bufio.NewReader(os.Stdin))
	if err != nil {
		os.Exit(5)
	}
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		os.Exit(6)
	}
	response := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"content":"generation-%d","output_path":"page.out"}}`, request.ID, call)
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(response), response)
	os.Exit(0)
}

func readCLILSPFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]))
			if err != nil {
				return nil, err
			}
		}
	}
	payload := make([]byte, contentLength)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}
