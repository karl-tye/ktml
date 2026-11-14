package lsp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/plugin/alpine"
	"github.com/karl-tye/ktml/internal/plugin/gohtml"
	"github.com/karl-tye/ktml/internal/symbolindex"
)

// generatedWorkspace writes components files under dirs directories. Each
// component takes a record of rows, loops over them, and calls the
// components of the three files before it; the first file is imported by
// every tenth file, a hub whose edits reindex many importers.
func generatedWorkspace(t *testing.T, dirs, files int) (root, hub, leaf string) {
	t.Helper()
	root = t.TempDir()
	write := func(path, source string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "ktml.config.json"), "{}")
	write(filepath.Join(root, "schema", "rows.ktmlschema"), "record Row [\n\tid: string,\n\tlabel: string,\n\tcount: number,\n]\n\nrecord Table [\n\ttitle: string,\n\trows: [Row],\n]\n")
	name := func(i int) string { return fmt.Sprintf("C%04d", i) }
	file := func(i int) string {
		return filepath.Join(root, fmt.Sprintf("d%02d", i%dirs), fmt.Sprintf("c%04d.ktml", i))
	}
	for i := 0; i < files; i++ {
		var source strings.Builder
		relative := func(path string) string {
			r, _ := filepath.Rel(filepath.Dir(file(i)), path)
			if !strings.HasPrefix(r, ".") {
				r = "./" + r
			}
			return r
		}
		fmt.Fprintf(&source, "import { Table, Row } from %q\n", relative(filepath.Join(root, "schema", "rows.ktmlschema")))
		var callees []int
		for back := 1; back <= 3 && i-back >= 0; back++ {
			callees = append(callees, i-back)
		}
		if i%10 == 9 && i > 3 {
			callees = append(callees, 0)
		}
		for _, callee := range callees {
			fmt.Fprintf(&source, "import { %s } from %q\n", name(callee), relative(file(callee)))
		}
		fmt.Fprintf(&source, "\n%s [table: Table, note?: string] {\n", name(i))
		source.WriteString("\tsection class=\"flex gap-2 p-4\" x-data=`{ open: false, toggle() { this.open = !this.open } }` {\n")
		source.WriteString("\t\th2 class=\"text-lg font-bold\" \"{table.title}\"\n")
		source.WriteString("\t\tbutton @click=\"toggle()\" \"Toggle\"\n")
		source.WriteString("\t\tfor row in table.rows key row.id {\n\t\t\tp class=\"text-sm\" \"{row.label} {row.count}\"\n\t\t}\n")
		for _, callee := range callees {
			fmt.Fprintf(&source, "\t\t%s table=\"{table}\"\n", name(callee))
		}
		source.WriteString("\t}\n}\n")
		write(file(i), source.String())
	}
	return root, file(0), file(files - 1)
}

// heapInUse is the host heap after a collection; plugin linear memories are
// Go allocations, so it counts the guests too.
func heapInUse() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapInuse
}

type indexMeasurement struct {
	files                int
	initial              time.Duration
	heap, indexHeap      uint64
	leafEdit, hubEdit    time.Duration
	references, hubCalls int
	stats                symbolindex.Stats
}

// measureIndex serves a project, waits for its initial index, and times an
// edit of a leaf document and of a hub that many documents import, each up
// to the answer of a references request that needs it indexed.
func measureIndex(t *testing.T, root, hub, leaf string, settings map[string]map[string]any, names ...string) indexMeasurement {
	t.Helper()
	files := 0
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() && path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if err == nil && strings.HasSuffix(path, ".ktml") {
			files++
		}
		return nil
	})
	for _, name := range names {
		firstPartyPlugin(t, name)
	}
	before := heapInUse()
	start := time.Now()
	client := startConfiguredHost(t, root, settings, names...)
	hubURI := "file://" + hub
	hubSource, err := os.ReadFile(hub)
	if err != nil {
		t.Fatal(err)
	}
	declaration := hubDeclaration(t, hubSource)
	references := func() []any {
		response := client.request("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": hubURI}, "position": declaration, "context": map[string]any{"includeDeclaration": false}})
		result, _ := response["result"].([]any)
		return result
	}
	first := references()
	measurement := indexMeasurement{files: files, initial: time.Since(start), heap: heapInUse() - min(before, heapInUse()), references: len(first)}

	edit := func(path string, version int) time.Duration {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		uri := "file://" + path
		client.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "version": version, "languageId": "ktml", "text": string(source)}}})
		client.request("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": hubURI}, "position": declaration, "context": map[string]any{"includeDeclaration": false}})
		edited := string(source) + "\n"
		begin := time.Now()
		client.send(map[string]any{"method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "version": version + 1}, "contentChanges": []any{map[string]any{"text": edited}}}})
		measurement.hubCalls = len(references())
		return time.Since(begin)
	}
	measurement.leafEdit = edit(leaf, 1)
	measurement.hubEdit = edit(hub, 1)
	measurement.indexHeap, measurement.stats = indexFootprint(t, root)
	return measurement
}

// indexFootprint is the host index's own heap and contents for a project:
// gohtml's and Alpine's workspace contributions, computed in process and
// applied to a fresh index.
func indexFootprint(t *testing.T, root string) (uint64, symbolindex.Stats) {
	t.Helper()
	t.Chdir(root)
	var answers [][]byte
	for _, handle := range []func(string, []byte) ([]byte, error){gohtml.HandleLSP, alpine.HandleLSP} {
		raw, err := handle(symbolindex.Method, []byte(`{"id":1,"workspace":true}`))
		if err != nil {
			t.Fatal(err)
		}
		answers = append(answers, raw)
	}
	before := heapInUse()
	index := symbolindex.New()
	for i, raw := range answers {
		var response struct {
			Result symbolindex.Result `json:"result"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		index.Apply(strconv.Itoa(i), response.Result)
	}
	after := heapInUse()
	runtime.KeepAlive(answers)
	stats := index.Stats()
	runtime.KeepAlive(index)
	return after - min(before, after), stats
}

// hubDeclaration is the position of the first component declaration.
func hubDeclaration(t *testing.T, source []byte) map[string]any {
	t.Helper()
	for line, text := range strings.Split(string(source), "\n") {
		if text != "" && text[0] >= 'A' && text[0] <= 'Z' && strings.Contains(text, "[") {
			return map[string]any{"line": line, "character": 1}
		}
	}
	t.Fatal("no component declaration in the hub")
	return nil
}

func (m indexMeasurement) String() string {
	return fmt.Sprintf("%d files: initial index %v, heap %.1f MiB (index alone %.1f MiB for %d documents, %d symbols, %d references), leaf edit %v, hub edit %v, hub references %d",
		m.files, m.initial.Round(time.Millisecond), float64(m.heap)/(1<<20), float64(m.indexHeap)/(1<<20), m.stats.Documents, m.stats.Symbols, m.stats.References,
		m.leafEdit.Round(time.Millisecond), m.hubEdit.Round(time.Millisecond), m.references)
}

func TestSymbolIndex_GIVENALargeGeneratedWorkspace_WHENIndexedAndEdited_THENStaysWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the plugins and indexes 400 documents")
	}
	root, hub, leaf := generatedWorkspace(t, 20, 400)
	measurement := measureIndex(t, root, hub, leaf, nil, "gohtml", "alpine")
	t.Log(measurement)
	// Every tenth file and the three after it call the hub.
	if measurement.references < 40 {
		t.Errorf("hub references = %d, want its importers' calls", measurement.references)
	}
	budgets := []struct {
		name        string
		got, budget time.Duration
	}{
		{"initial index", measurement.initial, 60 * time.Second},
		{"leaf edit", measurement.leafEdit, 5 * time.Second},
		{"hub edit", measurement.hubEdit, 10 * time.Second},
	}
	for _, budget := range budgets {
		if budget.got > budget.budget {
			t.Errorf("%s took %v, over its %v budget", budget.name, budget.got, budget.budget)
		}
	}
}

// TestSymbolIndex_GIVENAHostApplicationsTree_WHENIndexedAndEdited_THENReportsTheCost
// measures a large host application's tree read-only when KTML_HOST_DIR
// names its root. KTML_HOST_HUB names a document, relative to the root,
// that many others import (default internal/ui/data.ktml), and
// KTML_HOST_LEAF one that nothing imports. The edits are editor overlays:
// no file is written.
func TestSymbolIndex_GIVENAHostApplicationsTree_WHENIndexedAndEdited_THENReportsTheCost(t *testing.T) {
	root := os.Getenv("KTML_HOST_DIR")
	if root == "" {
		t.Skip("KTML_HOST_DIR is not set")
	}
	hubPath, leafPath := os.Getenv("KTML_HOST_HUB"), os.Getenv("KTML_HOST_LEAF")
	if hubPath == "" {
		hubPath = filepath.Join("internal", "ui", "data.ktml")
	}
	if leafPath == "" {
		t.Skip("KTML_HOST_LEAF is not set")
	}
	hub := filepath.Join(root, filepath.FromSlash(hubPath))
	leaf := filepath.Join(root, filepath.FromSlash(leafPath))
	for _, path := range []string{hub, leaf} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("the host tree has no %s: %v", path, err)
		}
	}
	measurement := measureIndex(t, root, hub, leaf, nil, "gohtml", "alpine")
	t.Log("host:", measurement)
	if measurement.references == 0 {
		t.Errorf("no references to %s", hub)
	}
}
