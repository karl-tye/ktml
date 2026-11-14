package buildstate

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/karl-tye/ktml/internal/compiler"
)

type FileEntry struct {
	Hash             string            `json:"hash"`
	PrimaryOutput    string            `json:"primary_output"`
	AuxiliaryOutputs []string          `json:"auxiliary_outputs"`
	OutputHashes     map[string]string `json:"output_hashes,omitempty"`
	// Writer names the generator that wrote the outputs: a plugin, or
	// NativeWriterName. Stale cleanup is confined to that writer's roots.
	Writer string `json:"writer,omitempty"`
}

type Manifest struct {
	Files map[string]FileEntry `json:"files"`
}

type Output struct {
	Path        string
	Content     []byte
	Description string
}

var testBeforeRootMutation func(operation, path string)

func testRootMutation(operation, path string) {
	if testBeforeRootMutation != nil {
		testBeforeRootMutation(operation, path)
	}
}

// WriteOutput writes one output for writer, confined as PublishBatch
// confines publications.
func WriteOutput(root string, writer *Writer, output Output) error {
	rootFS, _, _, err := openTrustedRoot(root)
	if err != nil {
		return fmt.Errorf("open project root: %w", err)
	}
	defer rootFS.Close()
	resolver := newOutputResolver(rootFS)
	defer resolver.Close()
	resolved, err := resolver.resolve(output.Path)
	if err != nil {
		return fmt.Errorf("invalid %s path %q: %w", output.Description, output.Path, err)
	}
	if err := checkOutput(writer, "write", resolved.stored); err != nil {
		return err
	}
	testRootMutation("output backup", resolved.stored)
	backup, err := backupOutput(resolved)
	if err != nil {
		return fmt.Errorf("failed to write %s %s: %w", output.Description, resolved.stored, err)
	}
	if backup.exists {
		if err := checkExisting(writer, "overwrite", resolved.stored, backup.content); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o644)
	if backup.exists {
		mode = backup.mode.Perm()
	}
	testRootMutation("output write", resolved.stored)
	if err := atomicWriteOutput(resolved, output.Content, mode, ".ktml-output-*.tmp"); err != nil {
		return fmt.Errorf("failed to write %s %s: %w", output.Description, resolved.stored, err)
	}
	return nil
}

const (
	cacheSchemaVersion = "build-state-v3"
	compilerVersion    = "ktml-compiler-v1"
	pluginABIVersion   = "ktml-wasm-abi-v2"
)

func Load(path string) (*Manifest, error) {
	rootFS, relative, err := openRootForSave(path)
	if err != nil {
		return nil, err
	}
	defer rootFS.Close()
	state, err := openRootedState(rootFS, relative, false)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{Files: make(map[string]FileEntry)}, nil
		}
		if isRootEscape(err) {
			return nil, fmt.Errorf("build state path is outside project root: %w", err)
		}
		return nil, err
	}
	defer state.Close()
	testRootMutation("build state load", relative)
	if err := state.verify(); err != nil {
		return nil, err
	}
	data, err := state.parent.ReadFile(state.base)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{Files: make(map[string]FileEntry)}, nil
		}
		if isRootEscape(err) {
			return nil, fmt.Errorf("build state path is outside project root: %w", err)
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Files == nil {
		m.Files = make(map[string]FileEntry)
	}
	return &m, nil
}

func Save(path string, m *Manifest) error {
	rootFS, relative, err := openRootForSave(path)
	if err != nil {
		return err
	}
	defer rootFS.Close()
	return save(rootFS, relative, m)
}

func save(rootFS *os.Root, path string, m *Manifest) error {
	state, err := openRootedState(rootFS, path, true)
	if err != nil {
		return err
	}
	defer state.Close()
	return saveState(state, m)
}

func saveState(state *rootedState, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := state.parent.Lstat(state.base); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("build state is not a regular file")
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	testRootMutation("build state save", state.path)
	if err := state.verify(); err != nil {
		return err
	}
	return atomicWrite(state.parent, state.base, data, mode, ".state-*.tmp")
}

func Hash(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

func FallbackOutputPath(root, source, suffix string) (string, error) {
	rootFS, _, canonicalRoot, err := openTrustedRoot(root)
	if err != nil {
		return "", fmt.Errorf("open project root: %w", err)
	}
	defer rootFS.Close()
	rootInfo, err := rootFS.Stat(".")
	if err != nil {
		return "", fmt.Errorf("inspect project root: %w", err)
	}
	caseInsensitive, err := caseInsensitiveFilesystemAt(rootFS, rootInfo)
	if err != nil {
		return "", fmt.Errorf("determine filesystem case sensitivity: %w", err)
	}
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	suppliedSource, err := os.Open(absoluteSource)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	defer suppliedSource.Close()
	suppliedInfo, err := suppliedSource.Stat()
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	canonicalSource, err := filepath.EvalSymlinks(absoluteSource)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	canonicalFile, err := os.Open(canonicalSource)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	canonicalInfo, canonicalErr := canonicalFile.Stat()
	canonicalFile.Close()
	if canonicalErr != nil {
		return "", fmt.Errorf("resolve source path: %w", canonicalErr)
	}
	if !os.SameFile(suppliedInfo, canonicalInfo) {
		return "", fmt.Errorf("source path changed during resolution")
	}
	relative, err := relativeWithin(canonicalRoot, canonicalSource, caseInsensitive)
	if err != nil {
		return "", fmt.Errorf("source path is outside project root")
	}
	rootedSource, err := rootFS.Open(relative)
	if err != nil {
		if isRootEscape(err) {
			return "", fmt.Errorf("source path is outside project root: %w", err)
		}
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	defer rootedSource.Close()
	rootedInfo, err := rootedSource.Stat()
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	if !os.SameFile(rootedInfo, suppliedInfo) {
		return "", fmt.Errorf("source path is outside project root")
	}
	return filepath.Clean(relative) + suffix, nil
}

func relativeWithin(root, path string, caseInsensitive bool) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return relative, nil
	}
	if !caseInsensitive || len(path) <= len(root) || !strings.EqualFold(path[:len(root)], root) || !os.IsPathSeparator(path[len(root)]) {
		return "", fmt.Errorf("outside root")
	}
	return filepath.Clean(path[len(root)+1:]), nil
}

func GenerationHash(workspaceHash string, pluginContent []byte, configuration any) (string, error) {
	configurationJSON, err := json.Marshal(configuration)
	if err != nil {
		return "", fmt.Errorf("marshal generation cache configuration: %w", err)
	}
	payload := struct {
		Schema        string          `json:"schema"`
		Compiler      string          `json:"compiler"`
		PluginABI     string          `json:"plugin_abi"`
		Workspace     string          `json:"workspace"`
		Plugin        string          `json:"plugin"`
		Configuration json.RawMessage `json:"configuration"`
	}{
		Schema:        cacheSchemaVersion,
		Compiler:      compilerVersion,
		PluginABI:     pluginABIVersion,
		Workspace:     workspaceHash,
		Plugin:        Hash(pluginContent),
		Configuration: configurationJSON,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal generation cache identity: %w", err)
	}
	return Hash(encoded), nil
}

func HashWorkspace(snapshot *compiler.Snapshot, root string) string {
	visited := make(map[string]bool)
	pending := []string{root}
	for len(pending) > 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if visited[path] {
			continue
		}
		visited[path] = true
		pending = append(pending, snapshot.Dependencies(path)...)
	}
	if len(visited) == 1 {
		return Hash(snapshot.Source(root))
	}
	paths := make([]string, 0, len(visited))
	for path := range visited {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(snapshot.Source(path))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func CacheHit(statePath string, manifest *Manifest, source, hash string) (bool, error) {
	return cacheHit(statePath, manifest, source, hash, true)
}

// CacheChecker answers CacheHit for many sources of one manifest that does
// not change meanwhile, validating the manifest's output ownership once
// instead of on every hit. It is safe for concurrent use.
type CacheChecker struct {
	statePath string
	manifest  *Manifest
	once      sync.Once
	err       error
}

// NewCacheChecker returns a CacheChecker for manifest, saved at statePath.
func NewCacheChecker(statePath string, manifest *Manifest) *CacheChecker {
	return &CacheChecker{statePath: statePath, manifest: manifest}
}

// Hit is CacheHit for source and hash.
func (c *CacheChecker) Hit(source, hash string) (bool, error) {
	if entry, ok := c.manifest.Files[source]; !ok || entry.Hash != hash {
		return false, nil
	}
	c.once.Do(func() {
		rootFS, _, _, state, err := openStateRoot(c.statePath)
		if err != nil {
			c.err = err
			return
		}
		defer rootFS.Close()
		if state != nil {
			defer state.Close()
		}
		resolver := newOutputResolver(rootFS)
		defer resolver.Close()
		_, c.err = outputOwners(resolver, c.manifest)
	})
	if c.err != nil {
		return false, c.err
	}
	return cacheHit(c.statePath, c.manifest, source, hash, false)
}

func cacheHit(statePath string, manifest *Manifest, source, hash string, checkOwners bool) (bool, error) {
	entry, ok := manifest.Files[source]
	if !ok || entry.Hash != hash {
		return false, nil
	}
	rootFS, _, _, state, err := openStateRoot(statePath)
	if err != nil {
		return false, err
	}
	defer rootFS.Close()
	if state != nil {
		defer state.Close()
	}
	resolver := newOutputResolver(rootFS)
	defer resolver.Close()
	if checkOwners {
		if _, err := outputOwners(resolver, manifest); err != nil {
			return false, err
		}
	}
	paths := append([]string{entry.PrimaryOutput}, entry.AuxiliaryOutputs...)
	if len(paths) == 0 || entry.PrimaryOutput == "" {
		return false, nil
	}
	for _, recordedPath := range paths {
		resolved, err := resolver.resolve(recordedPath)
		if errors.Is(err, errMissingOutputParent) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("invalid cached output path %q: %w", recordedPath, err)
		}
		want, ok := entry.OutputHashes[resolved.stored]
		if !ok {
			return false, nil
		}
		info, err := resolved.parent.root.Lstat(resolved.base)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, nil
		}
		content, err := resolved.parent.root.ReadFile(resolved.base)
		if err != nil {
			return false, err
		}
		if Hash(content) != want {
			return false, nil
		}
	}
	return true, nil
}

type fileBackup struct {
	exists  bool
	content []byte
	mode    os.FileMode
}

// Publication is one source's generated outputs and build-state entry.
type Publication struct {
	Source  string
	Entry   FileEntry
	Outputs []Output
	// Writer confines the outputs to its roots and is recorded in the
	// entry. Nil is the host itself, confined by the deny-list only.
	Writer *Writer
}

func Publish(statePath string, manifest *Manifest, source string, entry FileEntry, outputs []Output) error {
	return PublishBatch(statePath, manifest, []Publication{{Source: source, Entry: entry, Outputs: outputs}}, nil)
}

func Remove(statePath string, manifest *Manifest, sources ...string) error {
	return PublishBatch(statePath, manifest, nil, sources)
}

// PublishBatch writes every publication's outputs, deletes outputs no longer
// owned by any source, and saves build state as one transaction. Any failure
// restores every touched output and leaves the saved state and manifest unchanged.
//
// Every write and deletion is confined: no writer touches a path on the
// host's deny-list (.git, .ktml, configuration, KTML sources, handwritten Go),
// and a writer touches only its declared roots. Stale outputs of an entry are
// confined by the writer the entry records, found among the publications'
// writers and writers.
func PublishBatch(statePath string, manifest *Manifest, publications []Publication, removals []string, writers ...Writer) error {
	rootFS, _, stateRelative, state, err := openStateRoot(statePath)
	if err != nil {
		return err
	}
	defer rootFS.Close()
	if state != nil {
		defer state.Close()
	}
	known := make(map[string]*Writer, len(writers)+len(publications))
	for i := range writers {
		known[writers[i].Name] = &writers[i]
	}
	published := make(map[string]bool, len(publications))
	for _, publication := range publications {
		if publication.Writer != nil {
			known[publication.Writer.Name] = publication.Writer
		}
		if published[publication.Source] {
			return fmt.Errorf("source %q is published more than once", publication.Source)
		}
		published[publication.Source] = true
	}
	removedSources := make(map[string]bool, len(removals))
	for _, source := range removals {
		if published[source] {
			return fmt.Errorf("source %q is both published and removed", source)
		}
		if _, ok := manifest.Files[source]; ok {
			removedSources[source] = true
		}
	}
	if len(publications) == 0 && len(removedSources) == 0 {
		return nil
	}
	resolver := newOutputResolver(rootFS)
	defer resolver.Close()
	type preparedOutput struct {
		Output
		resolved *resolvedOutput
	}
	var prepared []preparedOutput
	entries := make(map[string]FileEntry, len(publications))
	batchOutputs := make(map[string]string)
	for _, publication := range publications {
		source := publication.Source
		outputPaths := make(map[string]string, len(publication.Outputs))
		start := len(prepared)
		for _, output := range publication.Outputs {
			resolved, err := resolver.resolve(output.Path)
			if err != nil {
				return fmt.Errorf("invalid %s path %q: %w", output.Description, output.Path, err)
			}
			if previous, exists := outputPaths[resolved.identity]; exists {
				return fmt.Errorf("output paths %q and %q refer to the same file", previous, output.Path)
			}
			if previous, exists := batchOutputs[resolved.identity]; exists {
				return fmt.Errorf("output %q is published by both %q and %q", output.Path, previous, source)
			}
			if err := checkOutput(publication.Writer, "write", resolved.stored); err != nil {
				return err
			}
			if current, readErr := resolved.parent.root.ReadFile(resolved.base); readErr == nil {
				if err := checkExisting(publication.Writer, "overwrite", resolved.stored, current); err != nil {
					return err
				}
			}
			outputPaths[resolved.identity] = output.Path
			output.Path = resolved.stored
			prepared = append(prepared, preparedOutput{Output: output, resolved: resolved})
		}
		for identity := range outputPaths {
			batchOutputs[identity] = source
		}
		entry, err := canonicalizeEntry(resolver, publication.Entry)
		if err != nil {
			return fmt.Errorf("invalid published build state: %w", err)
		}
		entryOutputs := make(map[string]bool, len(entry.AuxiliaryOutputs)+1)
		for _, path := range append([]string{entry.PrimaryOutput}, entry.AuxiliaryOutputs...) {
			if path == "" {
				continue
			}
			resolved, err := resolver.resolve(path)
			if err != nil {
				return fmt.Errorf("invalid published output path %q: %w", path, err)
			}
			if _, ok := outputPaths[resolved.identity]; !ok {
				return fmt.Errorf("published output path %q has no content", path)
			}
			entryOutputs[resolved.identity] = true
		}
		for identity, path := range outputPaths {
			if !entryOutputs[identity] {
				return fmt.Errorf("output path %q is not recorded in build state", path)
			}
		}
		entry.Writer = ""
		if publication.Writer != nil {
			entry.Writer = publication.Writer.Name
		}
		entry.OutputHashes = make(map[string]string, len(prepared)-start)
		for _, output := range prepared[start:] {
			entry.OutputHashes[output.resolved.stored] = Hash(output.Content)
		}
		entries[source] = entry
	}
	owners, err := outputOwners(resolver, manifest)
	if err != nil {
		return err
	}
	next := &Manifest{Files: make(map[string]FileEntry, len(manifest.Files)+len(entries))}
	for path, existing := range manifest.Files {
		if !removedSources[path] {
			next.Files[path] = existing
		}
	}
	for source, entry := range entries {
		next.Files[source] = entry
	}
	for identity, source := range batchOutputs {
		owner, owned := owners[identity]
		if !owned || owner == source || removedSources[owner] {
			continue
		}
		if _, republished := entries[owner]; republished {
			continue
		}
		path := ""
		for _, output := range prepared {
			if output.resolved.identity == identity {
				path = output.Path
				break
			}
		}
		return fmt.Errorf("output %q is owned by source %q", path, owner)
	}
	nextOwners, err := outputOwners(resolver, next)
	if err != nil {
		return err
	}

	backups := make(map[string]fileBackup)
	var changed []*resolvedOutput
	var removed []*resolvedOutput
	rollback := func(cause error) error {
		var rollbackErr error
		for i := len(changed) - 1; i >= 0; i-- {
			if err := restoreOutput(changed[i], backups[changed[i].identity]); err != nil && rollbackErr == nil {
				rollbackErr = err
			}
		}
		for i := len(removed) - 1; i >= 0; i-- {
			if err := restoreOutput(removed[i], backups[removed[i].identity]); err != nil && rollbackErr == nil {
				rollbackErr = err
			}
		}
		if rollbackErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", cause, rollbackErr)
		}
		return cause
	}

	for _, preparedOutput := range prepared {
		output := preparedOutput.Output
		resolved := preparedOutput.resolved
		current, err := resolved.parent.root.ReadFile(resolved.base)
		if err == nil && bytes.Equal(current, output.Content) {
			continue
		}
		if _, recorded := backups[resolved.identity]; !recorded {
			testRootMutation("output backup", resolved.stored)
			backup, backupErr := backupOutput(resolved)
			if backupErr != nil {
				return rollback(fmt.Errorf("failed to write %s %s: %w", output.Description, resolved.stored, backupErr))
			}
			backups[resolved.identity] = backup
			changed = append(changed, resolved)
		}
		mode := os.FileMode(0o644)
		if backups[resolved.identity].exists {
			mode = backups[resolved.identity].mode.Perm()
		}
		testRootMutation("output write", resolved.stored)
		if err := atomicWriteOutput(resolved, output.Content, mode, ".ktml-output-*.tmp"); err != nil {
			return rollback(fmt.Errorf("failed to write %s %s: %w", output.Description, resolved.stored, err))
		}
	}

	affected := make([]string, 0, len(entries)+len(removedSources))
	for source := range manifest.Files {
		if _, republished := entries[source]; republished || removedSources[source] {
			affected = append(affected, source)
		}
	}
	sort.Strings(affected)
	seen := make(map[string]bool)
	for _, source := range affected {
		oldEntry := manifest.Files[source]
		operation, description := "stale output deletion", "stale output"
		if removedSources[source] {
			operation, description = "source output deletion", "output"
		}
		for _, recordedPath := range append([]string{oldEntry.PrimaryOutput}, oldEntry.AuxiliaryOutputs...) {
			if recordedPath == "" {
				continue
			}
			resolved, err := resolver.resolve(recordedPath)
			if errors.Is(err, errMissingOutputParent) {
				continue
			}
			if err != nil {
				return rollback(fmt.Errorf("invalid %s path %q: %w", description, recordedPath, err))
			}
			if _, owned := nextOwners[resolved.identity]; owned || seen[resolved.identity] {
				continue
			}
			seen[resolved.identity] = true
			backup, err := backupOutput(resolved)
			if err != nil {
				return rollback(fmt.Errorf("failed to remove %s %s: %w", description, recordedPath, err))
			}
			if !backup.exists {
				continue
			}
			// Delete only what KTML wrote: a file without a recorded hash, or
			// one changed since (a hand edit, or a crafted state naming a
			// handwritten file), is left in place.
			if want, ok := oldEntry.OutputHashes[resolved.stored]; !ok || Hash(backup.content) != want {
				continue
			}
			var writer *Writer
			if oldEntry.Writer != "" {
				if writer = known[oldEntry.Writer]; writer == nil {
					return rollback(fmt.Errorf("build state records writer %q for %s %s, which is not configured: refusing to delete it", oldEntry.Writer, description, filepath.ToSlash(resolved.stored)))
				}
			}
			if err := checkOutput(writer, "delete", resolved.stored); err != nil {
				return rollback(err)
			}
			if err := checkExisting(writer, "delete", resolved.stored, backup.content); err != nil {
				return rollback(err)
			}
			backups[resolved.identity] = backup
			testRootMutation(operation, resolved.stored)
			if err := removeOutput(resolved); err != nil {
				return rollback(fmt.Errorf("failed to remove %s %s: %w", description, recordedPath, err))
			}
			removed = append(removed, resolved)
		}
	}

	if state == nil {
		state, err = createRootedState(rootFS, stateRelative)
		if err != nil {
			return rollback(fmt.Errorf("failed to save build state: %w", err))
		}
		defer state.Close()
	}
	if saveErr := saveState(state, next); saveErr != nil {
		return rollback(fmt.Errorf("failed to save build state: %w", saveErr))
	}
	manifest.Files = next.Files
	return nil
}

func backupFile(rootFS *os.Root, path string) (fileBackup, error) {
	info, err := rootFS.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileBackup{}, nil
		}
		return fileBackup{}, err
	}
	if !info.Mode().IsRegular() {
		return fileBackup{}, fmt.Errorf("not a regular file")
	}
	content, err := rootFS.ReadFile(path)
	if err != nil {
		return fileBackup{}, err
	}
	return fileBackup{exists: true, content: content, mode: info.Mode()}, nil
}

func (p *outputParent) verify() error {
	if p.name == "." {
		return nil
	}
	current, err := p.anchor.OpenRoot(p.name)
	if err != nil {
		return err
	}
	defer current.Close()
	info, err := current.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, p.info) {
		return fmt.Errorf("validated output parent changed")
	}
	return nil
}

func openRootNoFollow(anchor *os.Root, name string) (*os.Root, error) {
	anchorFile, err := anchor.Open(".")
	if err != nil {
		return nil, err
	}
	defer anchorFile.Close()
	directory, err := openDirectoryNoFollow(anchorFile, name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("directory cannot be acquired without following links (escapes from parent): %w", err)
	}
	defer directory.Close()
	directoryInfo, err := directory.Stat()
	if err != nil {
		return nil, err
	}
	if !directoryInfo.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}
	root, err := anchor.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	if !os.SameFile(directoryInfo, rootInfo) {
		root.Close()
		return nil, fmt.Errorf("directory changed during no-follow acquisition")
	}
	return root, nil
}

func backupOutput(output *resolvedOutput) (fileBackup, error) {
	if err := output.parent.verify(); err != nil {
		return fileBackup{}, err
	}
	return backupFile(output.parent.root, output.base)
}

func atomicWriteOutput(output *resolvedOutput, content []byte, mode os.FileMode, pattern string) error {
	if err := output.parent.verify(); err != nil {
		return err
	}
	return atomicWrite(output.parent.root, output.base, content, mode, pattern)
}

func removeOutput(output *resolvedOutput) error {
	if err := output.parent.verify(); err != nil {
		return err
	}
	return output.parent.root.Remove(output.base)
}

func restoreOutput(output *resolvedOutput, backup fileBackup) error {
	return restoreFile(output.parent.root, output.base, backup)
}

func restoreFile(rootFS *os.Root, path string, backup fileBackup) error {
	if !backup.exists {
		if err := rootFS.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return atomicWrite(rootFS, path, backup.content, backup.mode.Perm(), ".ktml-rollback-*.tmp")
}

func atomicWrite(rootFS *os.Root, path string, content []byte, mode os.FileMode, pattern string) (err error) {
	temporary, temporaryPath, err := createTemp(rootFS, filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if removeErr := rootFS.Remove(temporaryPath); removeErr != nil && !os.IsNotExist(removeErr) {
			if err != nil {
				err = fmt.Errorf("%w (temporary cleanup failed: %v)", err, removeErr)
			} else {
				err = fmt.Errorf("temporary cleanup failed: %w", removeErr)
			}
		}
	}()
	testRootMutation("atomic write temporary created", temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := rootFS.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	testRootMutation("atomic write committed", temporaryPath)
	return nil
}

func createTemp(rootFS *os.Root, dir, pattern string) (*os.File, string, error) {
	prefix, suffix := pattern, ""
	if wildcard := strings.LastIndexByte(pattern, '*'); wildcard >= 0 {
		prefix, suffix = pattern[:wildcard], pattern[wildcard+1:]
	}
	for range 100 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		path := filepath.Join(dir, prefix+hex.EncodeToString(random[:])+suffix)
		file, err := rootFS.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, path, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not create temporary file")
}

type rootedState struct {
	parent     *os.Root
	anchor     *os.Root
	parentName string
	parentInfo os.FileInfo
	base       string
	path       string
}

func openRootedState(rootFS *os.Root, path string, createParent bool) (*rootedState, error) {
	parentPath := filepath.Dir(path)
	parent, anchor, parentName, err := openStateParent(rootFS, parentPath, createParent)
	if err != nil {
		return nil, err
	}
	parentInfo, err := parent.Stat(".")
	if err != nil {
		parent.Close()
		anchor.Close()
		return nil, err
	}
	state := &rootedState{
		parent:     parent,
		anchor:     anchor,
		parentName: parentName,
		parentInfo: parentInfo,
		base:       filepath.Base(path),
		path:       path,
	}
	if info, statErr := state.parent.Lstat(state.base); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		state.Close()
		return nil, fmt.Errorf("build state path is outside project root: state file is a symlink")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		state.Close()
		return nil, statErr
	}
	return state, nil
}

func createRootedState(rootFS *os.Root, path string) (*rootedState, error) {
	parentPath := filepath.Dir(path)
	anchor, err := rootFS.OpenRoot(filepath.Dir(parentPath))
	if err != nil {
		return nil, err
	}
	parentName := filepath.Base(parentPath)
	testRootMutation("build state parent absent", parentPath)
	parent, err := createStateDirectory(anchor, parentName)
	if err != nil {
		anchor.Close()
		return nil, err
	}
	parentInfo, err := parent.Stat(".")
	if err != nil {
		parent.Close()
		anchor.Close()
		return nil, err
	}
	return &rootedState{
		parent:     parent,
		anchor:     anchor,
		parentName: parentName,
		parentInfo: parentInfo,
		base:       filepath.Base(path),
		path:       path,
	}, nil
}

func openStateParent(rootFS *os.Root, path string, create bool) (*os.Root, *os.Root, string, error) {
	clean := filepath.Clean(path)
	if clean == "." {
		parent, err := rootFS.OpenRoot(".")
		if err != nil {
			return nil, nil, "", err
		}
		anchor, err := rootFS.OpenRoot(".")
		if err != nil {
			parent.Close()
			return nil, nil, "", err
		}
		return parent, anchor, ".", nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, nil, "", fmt.Errorf("build state parent is outside project root")
	}

	current, err := rootFS.OpenRoot(".")
	if err != nil {
		return nil, nil, "", err
	}
	parts := strings.Split(clean, string(filepath.Separator))
	walked := "."
	for i, part := range parts {
		next, openErr := openRootNoFollow(current, part)
		if openErr != nil && create && os.IsNotExist(openErr) {
			testRootMutation("build state parent absent", filepath.Join(walked, part))
			next, openErr = createStateDirectory(current, part)
		}
		if openErr != nil {
			current.Close()
			return nil, nil, "", openErr
		}
		if i == len(parts)-1 {
			return next, current, part, nil
		}
		current.Close()
		current = next
		walked = filepath.Join(walked, part)
	}
	current.Close()
	return nil, nil, "", fmt.Errorf("invalid build state parent")
}

func createStateDirectory(anchor *os.Root, name string) (*os.Root, error) {
	for range 100 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		temporary := ".ktml-state-dir-" + hex.EncodeToString(random[:]) + ".tmp"
		if err := anchor.Mkdir(temporary, 0o755); err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, err
		}
		testRootMutation("build state temporary parent created", temporary)
		created, err := openRootNoFollow(anchor, temporary)
		if err != nil {
			return nil, fmt.Errorf("acquire build state temporary parent: %w", err)
		}
		createdInfo, err := created.Stat(".")
		if err != nil {
			created.Close()
			return nil, fmt.Errorf("inspect build state temporary parent: %w", err)
		}
		if err := anchor.Rename(temporary, name); err != nil {
			created.Close()
			return nil, fmt.Errorf("acquire build state parent: %w", err)
		}
		installed, err := openRootNoFollow(anchor, name)
		if err != nil {
			created.Close()
			return nil, fmt.Errorf("verify build state parent: %w", err)
		}
		installedInfo, statErr := installed.Stat(".")
		installed.Close()
		if statErr != nil {
			created.Close()
			return nil, fmt.Errorf("verify build state parent: %w", statErr)
		}
		if !os.SameFile(createdInfo, installedInfo) {
			created.Close()
			return nil, fmt.Errorf("created build state parent changed during acquisition")
		}
		return created, nil
	}
	return nil, fmt.Errorf("could not create temporary build state directory")
}

func (s *rootedState) Close() {
	_ = s.parent.Close()
	_ = s.anchor.Close()
}

func (s *rootedState) verify() error {
	if s.parentName == "." {
		return nil
	}
	current, err := openRootNoFollow(s.anchor, s.parentName)
	if err != nil {
		return err
	}
	defer current.Close()
	info, err := current.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, s.parentInfo) {
		return fmt.Errorf("validated build state parent changed")
	}
	return nil
}

func openTrustedRoot(path string) (*os.Root, string, string, error) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return nil, "", "", fmt.Errorf("secure rooted filesystem operations are unsupported on %s", runtime.GOOS)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", "", err
	}
	rootFS, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, "", "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		rootFS.Close()
		return nil, "", "", err
	}
	canonicalRoot, err := os.OpenRoot(canonical)
	if err != nil {
		rootFS.Close()
		return nil, "", "", err
	}
	rootInfo, rootErr := rootFS.Stat(".")
	canonicalInfo, canonicalErr := canonicalRoot.Stat(".")
	canonicalRoot.Close()
	if rootErr != nil {
		rootFS.Close()
		return nil, "", "", rootErr
	}
	if canonicalErr != nil {
		rootFS.Close()
		return nil, "", "", canonicalErr
	}
	if !os.SameFile(rootInfo, canonicalInfo) {
		rootFS.Close()
		return nil, "", "", fmt.Errorf("project root changed during acquisition")
	}
	testRootMutation("project root acquired", absolute)
	return rootFS, absolute, canonical, nil
}

func openStateRoot(statePath string) (*os.Root, string, string, *rootedState, error) {
	absoluteState, err := filepath.Abs(statePath)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("resolve build state path: %w", err)
	}
	rootFS, root, _, err := openTrustedRoot(filepath.Dir(filepath.Dir(absoluteState)))
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("open project root: %w", err)
	}
	stateRelative, err := filepath.Rel(root, absoluteState)
	if err != nil || stateRelative == ".." || strings.HasPrefix(stateRelative, ".."+string(filepath.Separator)) {
		rootFS.Close()
		return nil, "", "", nil, fmt.Errorf("build state path is outside project root")
	}
	state, err := openRootedState(rootFS, stateRelative, false)
	if err != nil && os.IsNotExist(err) {
		return rootFS, root, stateRelative, nil, nil
	}
	if err != nil {
		rootFS.Close()
		if isRootEscape(err) {
			return nil, "", "", nil, fmt.Errorf("build state path is outside project root: %w", err)
		}
		return nil, "", "", nil, fmt.Errorf("inspect build state path: %w", err)
	}
	return rootFS, root, stateRelative, state, nil
}

func openRootForSave(path string) (*os.Root, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	parent := filepath.Dir(absolute)
	anchor := parent
	if filepath.Base(parent) == ".ktml" {
		anchor = filepath.Dir(parent)
	}
	for {
		rootFS, openedAnchor, _, openErr := openTrustedRoot(anchor)
		if openErr == nil {
			relative, err := filepath.Rel(openedAnchor, absolute)
			if err != nil {
				rootFS.Close()
				return nil, "", err
			}
			return rootFS, relative, nil
		}
		if !os.IsNotExist(openErr) {
			return nil, "", openErr
		}
		next := filepath.Dir(anchor)
		if next == anchor {
			return nil, "", fmt.Errorf("no existing directory for %s", path)
		}
		anchor = next
	}
}

func isRootEscape(err error) bool {
	return err != nil && strings.Contains(err.Error(), "escapes from parent")
}

func canonicalizeEntry(resolver *outputResolver, entry FileEntry) (FileEntry, error) {
	if entry.PrimaryOutput != "" {
		resolved, err := resolver.resolve(entry.PrimaryOutput)
		if err != nil {
			return FileEntry{}, err
		}
		entry.PrimaryOutput = resolved.stored
	}
	for i, path := range entry.AuxiliaryOutputs {
		resolved, err := resolver.resolve(path)
		if err != nil {
			return FileEntry{}, err
		}
		entry.AuxiliaryOutputs[i] = resolved.stored
	}
	return entry, nil
}

type outputResolver struct {
	rootFS  *os.Root
	parents []*outputParent
}

type outputParent struct {
	root            *os.Root
	anchor          *os.Root
	name            string
	info            os.FileInfo
	identity        int
	caseInsensitive bool
}

type resolvedOutput struct {
	parent   *outputParent
	base     string
	stored   string
	identity string
}

func newOutputResolver(rootFS *os.Root) *outputResolver {
	return &outputResolver{rootFS: rootFS}
}

func (r *outputResolver) Close() {
	for _, parent := range r.parents {
		_ = parent.root.Close()
		_ = parent.anchor.Close()
	}
}

func resolveRootedDirectory(rootFS *os.Root, path string) (string, error) {
	pending := filepath.Clean(path)
	for range 255 {
		resolved := "."
		followed := false
		parts := strings.Split(pending, string(filepath.Separator))
		for i, part := range parts {
			candidate := filepath.Join(resolved, part)
			info, err := rootFS.Lstat(candidate)
			if err != nil {
				if isRootEscape(err) {
					return "", fmt.Errorf("outside project root: %w", err)
				}
				if os.IsNotExist(err) {
					return "", fmt.Errorf("%w: %w", errMissingOutputParent, err)
				}
				return "", err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				resolved = candidate
				continue
			}
			target, err := rootFS.Readlink(candidate)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				return "", fmt.Errorf("outside project root")
			}
			remainder := filepath.Join(parts[i+1:]...)
			pending = filepath.Clean(filepath.Join(filepath.Dir(candidate), target, remainder))
			if pending == ".." || strings.HasPrefix(pending, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("outside project root")
			}
			followed = true
			break
		}
		if !followed {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("too many symbolic links")
}

func (r *outputResolver) resolve(path string) (*resolvedOutput, error) {
	if path == "" || filepath.IsAbs(path) {
		return nil, fmt.Errorf("outside project root")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("outside project root")
	}
	canonicalParent, err := resolveRootedDirectory(r.rootFS, filepath.Dir(clean))
	if err != nil {
		return nil, err
	}
	parentRoot, err := r.rootFS.OpenRoot(canonicalParent)
	if err != nil {
		if isRootEscape(err) {
			return nil, fmt.Errorf("outside project root: %w", err)
		}
		return nil, err
	}
	parentInfo, err := parentRoot.Stat(".")
	if err != nil {
		parentRoot.Close()
		return nil, err
	}
	var parent *outputParent
	for _, known := range r.parents {
		if os.SameFile(parentInfo, known.info) {
			parent = known
			break
		}
	}
	if parent != nil {
		parentRoot.Close()
	} else {
		anchor, anchorErr := r.rootFS.OpenRoot(filepath.Dir(canonicalParent))
		if anchorErr != nil {
			parentRoot.Close()
			return nil, anchorErr
		}
		caseInsensitive, caseErr := caseInsensitiveFilesystemAt(parentRoot, parentInfo)
		if caseErr != nil {
			parentRoot.Close()
			anchor.Close()
			return nil, fmt.Errorf("determine filesystem case sensitivity: %w", caseErr)
		}
		parent = &outputParent{
			root:            parentRoot,
			anchor:          anchor,
			name:            filepath.Base(canonicalParent),
			info:            parentInfo,
			identity:        len(r.parents),
			caseInsensitive: caseInsensitive,
		}
		r.parents = append(r.parents, parent)
	}
	base := filepath.Base(clean)
	if info, statErr := parent.root.Lstat(base); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("outside project root: output is a symlink")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		if isRootEscape(statErr) {
			return nil, fmt.Errorf("outside project root: %w", statErr)
		}
		return nil, statErr
	}
	identityBase := base
	if parent.caseInsensitive {
		identityBase = strings.ToLower(identityBase)
	}
	return &resolvedOutput{
		parent:   parent,
		base:     base,
		stored:   filepath.Join(canonicalParent, base),
		identity: fmt.Sprintf("%d:%s", parent.identity, identityBase),
	}, nil
}

type filesystemCase struct {
	info        os.FileInfo
	insensitive bool
}

func caseInsensitiveFilesystemAt(parent *os.Root, parentInfo os.FileInfo) (caseInsensitive bool, err error) {
	rootedFilesystemCaseCache.Lock()
	for _, entry := range rootedFilesystemCaseCache.entries {
		if os.SameFile(parentInfo, entry.info) {
			rootedFilesystemCaseCache.Unlock()
			return entry.insensitive, nil
		}
	}
	rootedFilesystemCaseCache.Unlock()
	temporary, temporaryPath, err := createTemp(parent, ".", ".ktml-case-probe-a*.tmp")
	if err != nil {
		return false, err
	}
	testRootMutation("filesystem case probe created", temporaryPath)
	closed := false
	defer func() {
		if !closed {
			if closeErr := temporary.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		if removeErr := parent.Remove(temporaryPath); removeErr != nil && !os.IsNotExist(removeErr) && err == nil {
			err = removeErr
		}
		if err == nil {
			rootedFilesystemCaseCache.Lock()
			rootedFilesystemCaseCache.entries = append(rootedFilesystemCaseCache.entries, filesystemCase{info: parentInfo, insensitive: caseInsensitive})
			rootedFilesystemCaseCache.Unlock()
		}
	}()
	info, err := temporary.Stat()
	if err != nil {
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	closed = true
	base := filepath.Base(temporaryPath)
	alias := filepath.Join(filepath.Dir(temporaryPath), strings.Replace(base, "probe-a", "PROBE-A", 1))
	aliasInfo, statErr := parent.Stat(alias)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			originalInfo, originalErr := parent.Stat(temporaryPath)
			if originalErr != nil {
				return false, fmt.Errorf("verify case-sensitivity probe: %w", originalErr)
			}
			if !os.SameFile(info, originalInfo) {
				return false, fmt.Errorf("case-sensitivity probe changed during inspection")
			}
			return false, nil
		}
		return false, statErr
	}
	return os.SameFile(info, aliasInfo), nil
}

var rootedFilesystemCaseCache struct {
	sync.Mutex
	entries []filesystemCase
}

// errMissingOutputParent reports that a recorded output's directory no longer
// exists inside the project. Such an output is already removed: it cannot
// collide with a new output or be deleted. Escaping paths and symlinks are
// rejected before the missing directory is reached, so they never report it.
var errMissingOutputParent = errors.New("output directory does not exist")

func outputOwners(resolver *outputResolver, manifest *Manifest) (map[string]string, error) {
	owners := make(map[string]string)
	for source, entry := range manifest.Files {
		for _, path := range append([]string{entry.PrimaryOutput}, entry.AuxiliaryOutputs...) {
			if path == "" {
				continue
			}
			resolved, err := resolver.resolve(path)
			if errors.Is(err, errMissingOutputParent) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("invalid output path %q for source %q: %w", path, source, err)
			}
			if owner, exists := owners[resolved.identity]; exists && owner != source {
				return nil, fmt.Errorf("output %q is owned by both %q and %q", path, owner, source)
			}
			owners[resolved.identity] = source
		}
	}
	return owners, nil
}
