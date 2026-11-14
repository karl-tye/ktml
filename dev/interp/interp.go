// Package interp is the KTML development interpreter (ADR-0004). It renders
// the components of a validated workspace snapshot from the generated Hybrid H
// props values, with the same rt escaping primitives, write boundaries, and
// RenderError kinds as the Go that gogen generates. Development builds link it
// through the ktmldev boundary; production binaries must not.
//
// A Program compiles every component Document once into a tree of render
// operations. Swapping a changed Document recompiles only that Document, so a
// hot swap costs one compile of the files that changed.
package interp

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/gopkg"
	"github.com/karl-tye/ktml/rt"
)

// Options configure a Program.
type Options struct {
	// Development writes the Render Identity boundaries of the ktmldev
	// generated variant. Without it the output matches the production
	// variant.
	Development bool
	// Packages returns the Go import path of a source directory. The default
	// derives it from the nearest go.mod, as the CLI does.
	Packages func(dir string) (string, error)
}

// Program is an immutable compiled workspace. It is safe for concurrent use.
type Program struct {
	options    Options
	snapshot   *compiler.Snapshot
	components map[compiler.SymbolID]*component
	byGoID     map[string]*component
	files      map[string][]*component
	stories    map[compiler.SymbolID]*story
	storyFiles map[string][]compiler.SymbolID
}

// Load indexes the workspace rooted at root and compiles it.
func Load(root string, options Options) (*Program, error) {
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		return nil, err
	}
	return New(snapshot, options)
}

// New compiles every component Document of a valid snapshot.
func New(snapshot *compiler.Snapshot, options Options) (*Program, error) {
	return compileProgram(snapshot, options, nil, nil)
}

// Swap returns a Program for a newer snapshot of the same workspace. Only the
// changed files are recompiled; the compiled components of every other
// Document are reused. Callers must pass every file whose source changed.
func (p *Program) Swap(snapshot *compiler.Snapshot, changed []string) (*Program, error) {
	dirty := make(map[string]bool, len(changed))
	for _, file := range changed {
		path, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		dirty[path] = true
	}
	return compileProgram(snapshot, p.options, p, dirty)
}

func compileProgram(snapshot *compiler.Snapshot, options Options, previous *Program, dirty map[string]bool) (*Program, error) {
	if snapshot == nil || !snapshot.Valid() {
		return nil, fmt.Errorf("interp: workspace snapshot contains errors")
	}
	if options.Packages == nil {
		options.Packages = func(dir string) (string, error) {
			pkg, err := gopkg.Resolve(dir)
			return pkg.ImportPath, err
		}
	}
	program := &Program{
		options:    options,
		snapshot:   snapshot,
		components: make(map[compiler.SymbolID]*component),
		byGoID:     make(map[string]*component),
		files:      make(map[string][]*component),
	}
	importPaths := make(map[string]string)
	var files []string
	for _, declared := range snapshot.Components() {
		if _, seen := program.files[declared.File]; !seen {
			program.files[declared.File] = nil
			files = append(files, declared.File)
		}
	}
	sort.Strings(files)
	for _, file := range files {
		var compiled []*component
		if previous != nil && !dirty[file] {
			compiled = previous.files[file]
		}
		if compiled == nil {
			var err error
			compiled, err = compileDocument(snapshot, file, options, importPaths)
			if err != nil {
				return nil, err
			}
		}
		program.files[file] = compiled
		for _, model := range compiled {
			program.components[model.id] = model
			program.byGoID[model.goID] = model
		}
	}
	if err := program.compileStories(importPaths); err != nil {
		return nil, err
	}
	return program, nil
}

// Render renders a generated props value, which is itself the component in
// Hybrid H, through the interpreter. The component is found by the value's Go
// type, so it must be a generated component of this workspace.
func (p *Program) Render(ctx context.Context, w io.Writer, props ktml.Component) error {
	if story, ok := props.(StoryProps); ok {
		return p.RenderStory(ctx, w, story.Story, story.Overrides)
	}
	model, values, err := p.root(props)
	if err != nil {
		return err
	}
	writer, owned := rt.Wrap(w)
	return rt.Finish(writer, owned, p.render(ctx, writer, model, values))
}

// Component returns a ktml.Component that renders props through the
// interpreter.
func (p *Program) Component(props ktml.Component) ktml.Component {
	return ktml.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return p.Render(ctx, w, props)
	})
}

// root reads a props value into the frame of its component.
func (p *Program) root(props ktml.Component) (*component, []reflect.Value, error) {
	value := reflect.ValueOf(props)
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, nil, fmt.Errorf("interp: nil props")
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("interp: %T is not a generated component", props)
	}
	goID := value.Type().PkgPath() + "." + value.Type().Name()
	model, ok := p.byGoID[goID]
	if !ok {
		return nil, nil, fmt.Errorf("interp: no component %s in the workspace", goID)
	}
	values := make([]reflect.Value, model.slots)
	for index, field := range model.inputs {
		got := value.FieldByName(field.goName)
		if !got.IsValid() {
			if field.mode == ast.RequiredInput {
				return nil, nil, fmt.Errorf("interp: %s has no field %s for required input %s; rebuild the server", goID, field.goName, field.name)
			}
			continue
		}
		values[index] = got
	}
	return model, values, nil
}

// ComponentID returns the canonical "<import path>.<Name>" ID of a generated
// props value, and whether the program has its component.
func (p *Program) ComponentID(props ktml.Component) (string, bool) {
	if story, ok := props.(StoryProps); ok {
		_, known := p.stories[story.Story]
		return string(story.Story), known
	}
	value := reflect.ValueOf(props)
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "", false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return "", false
	}
	goID := value.Type().PkgPath() + "." + value.Type().Name()
	_, ok := p.byGoID[goID]
	return goID, ok
}

// Dependencies returns the IDs of the components a component renders,
// transitively through its component calls, including itself, in sorted
// order. A component the program does not have depends only on itself.
func (p *Program) Dependencies(componentID string) []string {
	if _, ok := p.stories[compiler.SymbolID(componentID)]; ok {
		return p.storyDependencies(compiler.SymbolID(componentID))
	}
	seen := map[string]bool{componentID: true}
	queue := []string{componentID}
	for len(queue) > 0 {
		model, ok := p.byGoID[queue[0]]
		queue = queue[1:]
		if !ok {
			continue
		}
		model.visitCalls(func(call *callOp) {
			if callee, ok := p.components[call.target]; ok && !seen[callee.goID] {
				seen[callee.goID] = true
				queue = append(queue, callee.goID)
			}
		})
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// FileComponents returns the IDs of the components a Document declares.
func (p *Program) FileComponents(file string) []string {
	path, err := filepath.Abs(file)
	if err != nil {
		return nil
	}
	var ids []string
	for _, model := range p.files[path] {
		ids = append(ids, model.goID)
	}
	for _, id := range p.storyFiles[path] {
		ids = append(ids, string(id))
	}
	return ids
}

// visitCalls reports the target of every component call in ops, including
// calls in branches, repetition bodies, and content fills.
func visitCalls(ops []op, visit func(*callOp)) {
	for _, operation := range ops {
		switch value := operation.(type) {
		case ifOp:
			visitCalls(value.then, visit)
			visitCalls(value.otherwise, visit)
		case *repetitionOp:
			visitCalls(value.body, visit)
		case *callOp:
			visit(value)
			for _, fill := range value.contents {
				visitCalls(fill.body, visit)
			}
		}
	}
}

// visitCalls visits every component call of the component's default and
// clause bodies, including those in content it passes.
func (model *component) visitCalls(visit func(*callOp)) {
	visitCalls(model.body, visit)
	if model.dispatch != nil {
		for _, clause := range model.dispatch.cases {
			visitCalls(clause.body, visit)
		}
		visitCalls(model.dispatch.fallback, visit)
	}
}

// CallTarget returns the ID of the component that the call at site (its
// c<n> Render Identity source) of the component callerID renders. A frame
// "<callerID>#<site>" of a Render Identity names that call.
func (p *Program) CallTarget(callerID, site string) (string, bool) {
	model, ok := p.byGoID[callerID]
	if !ok {
		return "", false
	}
	var target string
	model.visitCalls(func(call *callOp) {
		if call.site != site || target != "" {
			return
		}
		if callee, ok := p.components[call.target]; ok {
			target = callee.goID
		}
	})
	return target, target != ""
}
