package dev

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/dev/control"
	"github.com/karl-tye/ktml/internal/dev/livereload"
	"github.com/karl-tye/ktml/internal/dev/runner"
	"github.com/karl-tye/ktml/internal/dev/watcher"
)

// attributesEqual compares attribute entries, including the conditions and
// branches of conditional groups and the paths of spreads.
func attributesEqual(a, b []*ast.AttributeNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i, left := range a {
		right := b[i]
		if left.Name != right.Name || left.Value != right.Value || (left.Condition == nil) != (right.Condition == nil) || (left.Spread == nil) != (right.Spread == nil) {
			return false
		}
		if left.Spread != nil && left.Spread.Path.String() != right.Spread.Path.String() {
			return false
		}
		if (left.BagRange == nil) != (right.BagRange == nil) || !attributesEqual(left.Bag, right.Bag) {
			return false
		}
		if group := left.Condition; group != nil {
			other := right.Condition
			if group.Condition.String() != other.Condition.String() || group.ElseIf != other.ElseIf || (group.ElseKeywordRange == nil) != (other.ElseKeywordRange == nil) ||
				!attributesEqual(group.Then, other.Then) || !attributesEqual(group.Else, other.Else) {
				return false
			}
		}
	}
	return true
}

func nodesEqual(a, b ast.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Type() != b.Type() {
		return false
	}
	switch a := a.(type) {
	case *ast.TextNode:
		return a.Value == b.(*ast.TextNode).Value
	case *ast.DoctypeNode:
		return a.Name == b.(*ast.DoctypeNode).Name
	case *ast.HTMLCommentNode:
		return a.Text == b.(*ast.HTMLCommentNode).Text
	case *ast.RawTextNode:
		return a.Text == b.(*ast.RawTextNode).Text
	case *ast.ElementNode:
		bNode := b.(*ast.ElementNode)
		if a.Name != bNode.Name || len(a.Attributes) != len(bNode.Attributes) || len(a.Children) != len(bNode.Children) {
			return false
		}
		if !attributesEqual(a.Attributes, bNode.Attributes) {
			return false
		}
		for i, c := range a.Children {
			if !nodesEqual(c, bNode.Children[i]) {
				return false
			}
		}
		return true
	case *ast.RepetitionNode:
		bNode := b.(*ast.RepetitionNode)
		if a.Item != bNode.Item || a.Collection.String() != bNode.Collection.String() || a.Key.String() != bNode.Key.String() || len(a.Body) != len(bNode.Body) {
			return false
		}
		for i, c := range a.Body {
			if !nodesEqual(c, bNode.Body[i]) {
				return false
			}
		}
		return true
	case *ast.ConditionNode:
		bNode := b.(*ast.ConditionNode)
		return a.Condition.String() == bNode.Condition.String() && a.ElseIf == bNode.ElseIf &&
			(a.ElseKeywordRange == nil) == (bNode.ElseKeywordRange == nil) &&
			bodiesEqual(a.Then, bNode.Then) && bodiesEqual(a.Else, bNode.Else)
	case *ast.SlotNode:
		bNode := b.(*ast.SlotNode)
		return a.Name == bNode.Name && bodiesEqual(a.Body, bNode.Body)
	case *ast.ElementDecl:
		bNode := b.(*ast.ElementDecl)
		if a.Name != bNode.Name || len(a.Attributes) != len(bNode.Attributes) || len(a.Body) != len(bNode.Body) || a.Bodyless != bNode.Bodyless || (a.DefaultRange == nil) != (bNode.DefaultRange == nil) {
			return false
		}
		for i, attr := range a.Attributes {
			bAttr := bNode.Attributes[i]
			if attr.Name != bAttr.Name || attr.TypeDecl != bAttr.TypeDecl || attr.Kind != bAttr.Kind || attr.Mode != bAttr.Mode || attr.Value != bAttr.Value || scalarRaw(attr.Default) != scalarRaw(bAttr.Default) {
				return false
			}
		}
		for i, c := range a.Body {
			if !nodesEqual(c, bNode.Body[i]) {
				return false
			}
		}
		return true
	case *ast.RenderClause:
		bNode := b.(*ast.RenderClause)
		if a.Name != bNode.Name || a.Selector != bNode.Selector || a.Pattern.String() != bNode.Pattern.String() || len(a.Bindings) != len(bNode.Bindings) || len(a.Body) != len(bNode.Body) {
			return false
		}
		for i, binding := range a.Bindings {
			if binding.Field != bNode.Bindings[i].Field || binding.Alias != bNode.Bindings[i].Alias {
				return false
			}
		}
		for i, c := range a.Body {
			if !nodesEqual(c, bNode.Body[i]) {
				return false
			}
		}
		return true
	case *ast.ImportStmt:
		bNode := b.(*ast.ImportStmt)
		if a.Path != bNode.Path || len(a.Items) != len(bNode.Items) {
			return false
		}
		for i, item := range a.Items {
			if item != bNode.Items[i] {
				return false
			}
		}
		return true
	}
	return false
}

func bodiesEqual(a, b []ast.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !nodesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func scalarRaw(literal *ast.ScalarLiteral) string {
	if literal == nil {
		return ""
	}
	return literal.Raw
}

func schemasCompatibleForPatch(oldDecl, newDecl *ast.ElementDecl) bool {
	oldInputs := make(map[string]*ast.AttributeDecl)
	for _, input := range oldDecl.Attributes {
		if input.Kind != ast.OverrideDeclaration {
			oldInputs[input.Name] = input
		}
	}
	seen := make(map[string]bool, len(oldInputs))
	for _, input := range newDecl.Attributes {
		if input.Kind == ast.OverrideDeclaration {
			continue
		}
		oldInput, exists := oldInputs[input.Name]
		if !exists {
			if input.Mode != ast.OptionalInput && input.Mode != ast.DefaultedInput {
				return false
			}
			continue
		}
		if input.TypeDecl != oldInput.TypeDecl || input.Mode != oldInput.Mode {
			return false
		}
		seen[input.Name] = true
	}
	if len(seen) != len(oldInputs) {
		return false
	}
	// An undeclared children outlet declares optional children, so adding the
	// first one is an optional addition and removing the last one a removal.
	_, declared := oldInputs[compiler.ChildrenInput]
	return declared || !compiler.UsesChildrenOutlet(oldDecl.Body) || compiler.UsesChildrenOutlet(newDecl.Body)
}

// requiresRebuild reports whether any change to a source must regenerate,
// rebuild, restart, and reload rather than patch. Every Host Schema change does,
// because schemas define the generated Go types every dependent compiles against.
func requiresRebuild(file string) bool {
	return compiler.IsSchemaPath(file)
}

// Batch is one atomic generation unit: every source to generate, including
// transitive dependents of changed or removed sources, and every removed source.
type Batch struct {
	Generate []string
	Remove   []string
}

// GenerateFunc must publish every output and build-state change in a batch, or
// none of them.
type GenerateFunc func(Batch) error

// Swapper is the application's development control channel (ADR-0004): it
// swaps the interpreter program and returns rerendered root HTML.
type Swapper interface {
	Swap(ctx context.Context, request hmr.SwapRequest) (hmr.SwapResponse, error)
}

type Hub struct {
	lrServer         *livereload.Server
	runner           *runner.Runner
	targetPort       int
	generateCallback GenerateFunc
	asts             map[string]*ast.Document
	pending          map[string]bool
	control          Swapper
	generation       uint64
	// forceRestart makes reload restart the backend and fully reload even
	// when the batch could be patched; set for restart_watch batches.
	forceRestart bool
	// ready bounds the wait for a restarted backend; zero is
	// DefaultReadyTimeout.
	ready time.Duration
}

// DefaultReadyTimeout is how long a restarted backend may take to answer
// before the previous one is restored (dev.ready_timeout_ms overrides it).
const DefaultReadyTimeout = 5 * time.Second

// SetReadyTimeout sets how long a restarted backend may take to become ready.
func (h *Hub) SetReadyTimeout(timeout time.Duration) {
	h.ready = timeout
}

func (h *Hub) readyTimeout() time.Duration {
	if h.ready <= 0 {
		return DefaultReadyTimeout
	}
	return h.ready
}

// SetControl connects the Hub to the application's control channel. Without
// one, every compatible change rebuilds and reloads.
func (h *Hub) SetControl(control Swapper) {
	h.control = control
}

// showError shows a diagnostic in the browser overlay; the last working page
// and backend stay in place.
func (h *Hub) showError(message string) {
	if h.lrServer != nil {
		h.lrServer.ShowError(message)
	}
}

func validDocument(path string, content []byte) (*ast.Document, error) {
	result := compiler.CompileSyntax(path, content)
	if err := result.Err(); err != nil {
		return nil, err
	}
	snapshot, err := compiler.NewWorkspace(filepath.Dir(path)).Update(path, 0, content)
	if err != nil {
		return nil, err
	}
	if err := snapshot.Err(); err != nil {
		return nil, err
	}
	return result.Document, nil
}

func NewHub(lrServer *livereload.Server, r *runner.Runner, targetPort int, generateCallback GenerateFunc) *Hub {
	return &Hub{
		lrServer:         lrServer,
		runner:           r,
		targetPort:       targetPort,
		generateCallback: generateCallback,
		asts:             make(map[string]*ast.Document),
		pending:          make(map[string]bool),
	}
}

func (h *Hub) TrackFile(path string) error {
	return h.TrackFiles([]string{path})
}

// TrackFiles is TrackFile for each path, validating the documents
// concurrently. It records the documents before the first invalid one, in
// order, and returns that one's error.
func (h *Hub) TrackFiles(paths []string) error {
	documents := make([]*ast.Document, len(paths))
	errs := make([]error, len(paths))
	next := make(chan int)
	var wait sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), len(paths)) {
		wait.Go(func() {
			for index := range next {
				content, err := os.ReadFile(paths[index])
				if err != nil {
					errs[index] = err
					continue
				}
				documents[index], err = validDocument(paths[index], content)
				if err != nil {
					errs[index] = fmt.Errorf("validate %s: %w", paths[index], err)
				}
			}
		})
	}
	for index := range paths {
		next <- index
	}
	close(next)
	wait.Wait()
	for index, path := range paths {
		if errs[index] != nil {
			return errs[index]
		}
		h.asts[filepath.Clean(path)] = documents[index]
	}
	return nil
}

var idRegex = regexp.MustCompile(`\s+data-ktml-id="[^"]*"`)

// clausesEqual compares a component's Render Clauses in source order.
func clausesEqual(a, b []*ast.RenderClause) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !nodesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func getChangedComponent(oldDoc, newDoc *ast.Document) string {
	oldDecls := make(map[string][]*ast.ElementDecl)
	oldClauses := make(map[string][]*ast.RenderClause)
	var oldImports []ast.Node
	for _, n := range oldDoc.Nodes {
		if d, ok := n.(*ast.ElementDecl); ok {
			oldDecls[d.Name] = append(oldDecls[d.Name], d)
		} else if c, ok := n.(*ast.RenderClause); ok {
			oldClauses[c.Name] = append(oldClauses[c.Name], c)
		} else if _, ok := n.(*ast.ImportStmt); ok {
			oldImports = append(oldImports, n)
		}
	}
	newDecls := make(map[string][]*ast.ElementDecl)
	newClauses := make(map[string][]*ast.RenderClause)
	var newImports []ast.Node
	for _, n := range newDoc.Nodes {
		if d, ok := n.(*ast.ElementDecl); ok {
			newDecls[d.Name] = append(newDecls[d.Name], d)
		} else if c, ok := n.(*ast.RenderClause); ok {
			newClauses[c.Name] = append(newClauses[c.Name], c)
		} else if _, ok := n.(*ast.ImportStmt); ok {
			newImports = append(newImports, n)
		}
	}
	// A Host Schema state is a type change, never a patch.
	for _, nodes := range [][]ast.Node{oldDoc.Nodes, newDoc.Nodes} {
		for _, n := range nodes {
			if _, ok := n.(*ast.StateDecl); ok {
				return ""
			}
		}
	}

	var changed string
	changes := 0
	if len(oldImports) != len(newImports) {
		changes++
	} else {
		for i := range oldImports {
			if !nodesEqual(oldImports[i], newImports[i]) {
				changes++
				break
			}
		}
	}
	for name, newDList := range newDecls {
		oldDList, ok := oldDecls[name]
		if !ok {
			changes++
			slog.Debug("Component added", "name", name)
			continue
		}

		if len(newDList) != len(oldDList) {
			changes++
			slog.Debug("Component overrides count changed", "name", name)
			continue
		}

		// Clause Pattern and body edits keep the signature, so they are
		// compatible whenever the signature is.
		componentChanged := !clausesEqual(oldClauses[name], newClauses[name])
		compatible := true

		for i, newD := range newDList {
			oldD := oldDList[i]

			if !schemasCompatibleForPatch(oldD, newD) {
				compatible = false
			}

			if !nodesEqual(oldD, newD) {
				componentChanged = true
			}
		}

		if !componentChanged {
			continue
		}

		if compatible {
			changed = name
			changes++
			slog.Debug("Component schema compatible", "name", name)
		} else {
			changes++
			slog.Debug("Component schema incompatible", "name", name)
		}
	}
	for name := range oldDecls {
		if _, ok := newDecls[name]; !ok {
			changes++
			slog.Debug("Component removed", "name", name)
		}
	}

	slog.Debug("Changed components evaluation", "totalChanges", changes, "changedMatch", changed)
	if changes == 1 && changed != "" {
		return changed
	}
	return ""
}

func (h *Hub) Listen(ctx context.Context, events <-chan watcher.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			switch ev.Type {
			case watcher.EventRestart:
				h.restart(ctx, ev.Files)
			case watcher.EventReload:
				if len(ev.Files) > 0 || len(ev.Assets) == 0 || len(h.pending) > 0 {
					h.reload(ctx, ev.Files)
				}
				h.reloadAssets(ev.Assets)
			}
		}
	}
}

// restart handles a batch with a restart_watch change. KTML sources changed
// in the same batch are generated first, so the rebuild compiles their fresh
// output; the backend restarts and every page reloads once. A KTML source that
// fails to validate or generate stays pending with its overlay, and the
// backend still restarts for the other changes.
func (h *Hub) restart(ctx context.Context, files []string) {
	if h.runner == nil || h.lrServer == nil {
		h.reload(ctx, files)
		return
	}
	h.forceRestart = true
	restarted := h.reload(ctx, files)
	h.forceRestart = false
	if restarted {
		return
	}
	slog.Debug("Restarting runner and triggering full reload")
	if err := h.runner.Restart(ctx, h.backendReady); err != nil {
		slog.Error("Runner restart failed; keeping the last working backend", "error", err)
		h.showError(restartFailed(err))
		return
	}
	if len(h.pending) == 0 {
		h.lrServer.ClearError()
	}
	h.lrServer.FullReload()
}

// reloadAssets tells every page to reload changed reload_watch assets: a
// stylesheet is swapped in place, anything else reloads the page. A restart
// batch's full reload covers its assets.
func (h *Hub) reloadAssets(assets []string) {
	if len(assets) == 0 || h.lrServer == nil {
		return
	}
	paths := make([]string, len(assets))
	for index, asset := range assets {
		paths[index] = filepath.ToSlash(filepath.Clean(asset))
	}
	// An editor save may write a file more than once in a batch.
	slices.Sort(paths)
	paths = slices.Compact(paths)
	slog.Debug("Reloading assets", "paths", paths)
	h.lrServer.ReloadAssets(paths)
}

func restartFailed(err error) string {
	return fmt.Sprintf("Backend rebuild failed; the last working backend keeps serving.\n\n%v", err)
}

func (h *Hub) backendReady(ctx context.Context) error {
	return h.lrServer.WaitForTarget(ctx, h.readyTimeout())
}

// maxSettleRounds bounds the batches one reload runs while the sources it
// reads keep changing under it. A source still changing after them stays
// pending, and the watcher's events for its later changes start new batches.
const maxSettleRounds = 8

// reload runs batches until the output matches the sources on disk. A batch
// reads its sources before generation, which can take long enough for them
// to change again, as when a git stash during a commit's hooks briefly puts
// older sources back and then restores them. The watcher's events for such
// a change can be folded into the batch already running, or lost when
// inotify's queue overflows, so after each batch reload checks every source
// it read and runs another batch for those that changed since. It reports
// whether it restarted the backend, successfully or not.
func (h *Hub) reload(ctx context.Context, files []string) bool {
	force := h.forceRestart
	defer func() { h.forceRestart = force }()
	restarted := false
	for round := 1; ; round++ {
		read := make(map[string]sourceStamp)
		if h.reloadBatch(ctx, files, read) {
			restarted = true
			// One restart covers the restart_watch change.
			h.forceRestart = false
		}
		files = changedSources(read)
		if len(files) == 0 {
			return restarted
		}
		if round >= maxSettleRounds {
			slog.Warn("Sources keep changing while they are generated; regenerating on their next change", "files", files)
			for _, file := range files {
				h.pending[file] = true
			}
			return restarted
		}
		slog.Info("Sources changed while their batch was generated; regenerating them", "files", files)
	}
}

// sourceStamp is what a batch read of one source: whether it existed, and
// its size, modification time, and content hash.
type sourceStamp struct {
	exists  bool
	size    int64
	modTime int64
	hash    [sha256.Size]byte
}

// readSource reads a batch source and records what was read. The stamp's
// metadata is taken before the read, so a change during the read shows as
// a change afterwards.
func readSource(file string, read map[string]sourceStamp) ([]byte, error) {
	info, statErr := os.Stat(file)
	content, err := os.ReadFile(file)
	switch {
	case err == nil:
		stamp := sourceStamp{exists: true, hash: sha256.Sum256(content)}
		if statErr == nil {
			stamp.size, stamp.modTime = info.Size(), info.ModTime().UnixNano()
		}
		read[file] = stamp
	case os.IsNotExist(err):
		read[file] = sourceStamp{}
	}
	return content, err
}

// changedSources returns the sources whose file differs from what a batch
// read: written, replaced, removed, or created since.
func changedSources(read map[string]sourceStamp) []string {
	var changed []string
	for file, before := range read {
		now := make(map[string]sourceStamp, 1)
		if _, err := readSource(file, now); err != nil && !os.IsNotExist(err) {
			changed = append(changed, file)
			continue
		}
		if now[file] != before {
			changed = append(changed, file)
		}
	}
	sort.Strings(changed)
	return changed
}

// reloadBatch prepares one batch from the changed files, any files left over
// from an earlier unpublished batch, and every transitive dependent of a
// changed or removed source, recording each source it reads in read.
// Generation, build state, the backend, the browser, and the AST baselines
// are published for the whole batch or not at all. It reports whether it
// restarted the backend, successfully or not.
func (h *Hub) reloadBatch(ctx context.Context, files []string, read map[string]sourceStamp) bool {
	fullReload := false
	var patchedComponent string
	var patchedFile string
	patchChanges := 0
	var fullReloadReason []string
	pendingASTs := make(map[string]*ast.Document)
	sources := make(map[string][]byte)
	var removedFiles []string

	batchFiles := make(map[string]bool, len(files)+len(h.pending))
	for _, rawFile := range files {
		file := filepath.Clean(rawFile)
		// Stories are Component Explorer fixtures; the application never
		// renders them. Hidden files are editor and formatter temp copies
		// (such as conform's .conform.<n>.page.ktml), never sources.
		if (strings.HasSuffix(file, ".ktml") || compiler.IsSchemaPath(file)) && !compiler.IsStoryPath(file) && !strings.HasPrefix(filepath.Base(file), ".") {
			batchFiles[file] = true
		}
	}
	for file := range h.pending {
		batchFiles[file] = true
	}
	if len(batchFiles) == 0 {
		return false
	}
	fail := func() {
		for file := range batchFiles {
			h.pending[file] = true
		}
	}

	for _, file := range sortedKeys(batchFiles) {
		slog.Debug("Parsing changed KTML file", "file", file)
		content, err := readSource(file, read)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Error("Failed to read changed file", "file", file, "error", err)
				fail()
				return false
			}
			removedFiles = append(removedFiles, file)
			fullReload = true
			fullReloadReason = append(fullReloadReason, fmt.Sprintf("removed_file:%s", file))
			continue
		}
		newDoc, err := validDocument(file, content)
		if err != nil {
			slog.Error("Keeping last valid KTML after validation failure", "file", file, "error", err)
			h.showError(err.Error())
			fail()
			return false
		}
		sources[file] = content
		if requiresRebuild(file) {
			slog.Debug("Host Schema changed, marking full reload", "file", file)
			fullReload = true
			fullReloadReason = append(fullReloadReason, fmt.Sprintf("host_schema_change:%s", file))
		} else if oldDoc, hasOld := h.asts[file]; hasOld {
			compName := getChangedComponent(oldDoc, newDoc)
			if compName != "" {
				patchedComponent = compName
				patchedFile = file
				patchChanges++
				slog.Debug("Identified component for patch", "component", compName)
			} else {
				slog.Debug("Multiple changes or non-subset component change, marking full reload")
				fullReload = true
				fullReloadReason = append(fullReloadReason, fmt.Sprintf("complex_change:%s", file))
			}
		} else {
			slog.Debug("No old AST cache for file, marking full reload", "file", file)
			fullReload = true
			fullReloadReason = append(fullReloadReason, fmt.Sprintf("new_file:%s", file))
		}
		pendingASTs[file] = newDoc
	}

	removed := make(map[string]bool, len(removedFiles))
	for _, file := range removedFiles {
		removed[file] = true
	}
	for _, file := range h.dependents(batchFiles, pendingASTs, removed) {
		content, err := readSource(file, read)
		if err != nil {
			slog.Error("Failed to read dependent file", "file", file, "error", err)
			fail()
			return false
		}
		document, err := validDocument(file, content)
		if err != nil {
			slog.Error("Keeping last working output after dependent validation failure", "file", file, "error", err)
			h.showError(err.Error())
			fail()
			return false
		}
		sources[file] = content
		slog.Debug("Regenerating dependent", "file", file)
		pendingASTs[file] = document
	}

	if h.forceRestart {
		fullReload = true
		fullReloadReason = append(fullReloadReason, "restart_watch")
	}
	if patchChanges > 1 {
		fullReload = true
		fullReloadReason = append(fullReloadReason, "multiple_patch_changes")
	}
	if !fullReload && patchedComponent == "" && len(pendingASTs) == 0 {
		return false
	}
	if !fullReload && patchedComponent != "" && h.lrServer == nil {
		slog.Error("lrServer is nil, cannot hot reload")
		fail()
		return false
	}

	batch := Batch{Generate: sortedKeys(pendingASTs), Remove: removedFiles}
	if err := h.generateCallback(batch); err != nil {
		slog.Error("Generate failed during reload preparation; keeping the last working output", "error", err)
		h.showError(err.Error())
		fail()
		return false
	}

	published := false
	restarted := false
	if !fullReload && patchedComponent != "" {
		slog.Debug("Attempting hot reload", "component", patchedComponent, "file", patchedFile)
		if reason := h.patch(ctx, batch.Generate, sources); reason != "" {
			fullReload = true
			fullReloadReason = append(fullReloadReason, reason)
		} else {
			published = true
		}
	}

	if fullReload {
		slog.Debug("Full reload triggered", "reasons", fullReloadReason)
		published = h.lrServer != nil
		if published && h.runner != nil {
			restarted = true
			if err := h.runner.Restart(ctx, h.backendReady); err != nil {
				slog.Error("Runner restart failed; keeping the last working backend", "error", err)
				if h.forceRestart {
					h.showError(restartFailed(err))
				}
				published = false
			}
		} else if published {
			if err := h.backendReady(ctx); err != nil {
				slog.Error("Backend did not become ready before reload", "error", err)
				published = false
			}
		}
		if published {
			h.lrServer.ClearError()
			h.lrServer.FullReload()
		}
	}

	if !published {
		fail()
		return restarted
	}
	if !fullReload && h.lrServer != nil {
		h.lrServer.ClearError()
	}
	for file, document := range pendingASTs {
		h.asts[file] = document
	}
	for _, file := range removedFiles {
		delete(h.asts, file)
	}
	clear(h.pending)
	return restarted
}

// patch swaps the application's interpreter program with the batch sources and
// broadcasts each rerendered root to its page session. It returns the reason a
// full rebuild and reload is needed instead, or "" when the patches were
// published. Any failed rerender reloads every page: the rebuild restarts the
// application and empties its registry.
func (h *Hub) patch(ctx context.Context, files []string, sources map[string][]byte) string {
	if h.control == nil {
		return "no_control_channel"
	}
	h.generation++
	request := hmr.SwapRequest{Generation: h.generation}
	for _, file := range files {
		path, err := filepath.Abs(file)
		if err != nil {
			return "swap_path:" + file
		}
		request.Files = append(request.Files, hmr.SwapFile{Path: path, Source: string(sources[file])})
	}
	response, err := h.control.Swap(ctx, request)
	if err != nil {
		var diagnostic *control.DiagnosticError
		if errors.As(err, &diagnostic) {
			slog.Error("The interpreter rejected the change; rebuilding", "error", diagnostic.Message)
			h.showError(diagnostic.Message)
			return "interpreter_diagnostic"
		}
		slog.Warn("Live patching is unavailable; rebuilding", "error", err)
		return "swap_unavailable"
	}
	if len(response.Reload) > 0 {
		slog.Warn("A live root failed to rerender; rebuilding and reloading", "sessions", len(response.Reload))
		return "rerender_failed"
	}
	for _, patch := range response.Patches {
		h.lrServer.Patch(patch.Session, patch.Identity, patch.HTML, patch.Document)
	}
	slog.Debug("Published live patches", "patches", len(response.Patches), "generation", response.Generation)
	return ""
}

// dependents returns tracked sources that transitively import a batch source,
// excluding sources already in the batch or removed by it.
func (h *Hub) dependents(batchFiles map[string]bool, pendingASTs map[string]*ast.Document, removed map[string]bool) []string {
	keys := make(map[string]string, len(h.asts))
	importers := make(map[string][]string)
	for file, document := range h.asts {
		if removed[file] {
			continue
		}
		if pending, ok := pendingASTs[file]; ok {
			document = pending
		}
		canonical, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		keys[canonical] = file
		for _, node := range document.Nodes {
			imp, ok := node.(*ast.ImportStmt)
			if !ok {
				continue
			}
			dependency, err := compiler.ResolveImportPath(canonical, imp.Path)
			if err != nil {
				continue
			}
			importers[dependency] = append(importers[dependency], canonical)
		}
	}
	var queue []string
	visited := make(map[string]bool)
	for file := range batchFiles {
		canonical, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		visited[canonical] = true
		queue = append(queue, canonical)
	}
	dependents := make(map[string]bool)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, importer := range importers[current] {
			if visited[importer] {
				continue
			}
			visited[importer] = true
			queue = append(queue, importer)
			if file := keys[importer]; !batchFiles[file] && !removed[file] {
				dependents[file] = true
			}
		}
	}
	return sortedKeys(dependents)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
