package explorer

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/storygen"
)

// ComponentInfo describes a component of the program for the component
// picker.
type ComponentInfo struct {
	// Name is the component's name; File its Document.
	Name string
	File string
	// Document reports a component that may render a whole document, whose
	// instances carry no marker on the page.
	Document bool
	// Story reports whether a story renders the component.
	Story bool
}

// component finds a component of the current program by its Go ID.
func (s *Server) component(componentID string) (*interp.Program, compiler.Component, bool) {
	program := s.registry().Program()
	if program == nil {
		return nil, compiler.Component{}, false
	}
	id, ok := program.Symbol(componentID)
	if !ok {
		return program, compiler.Component{}, false
	}
	component, ok := program.Snapshot().Component(id)
	return program, component, ok
}

// Describe returns what the picker shows of a component.
func (s *Server) Describe(componentID string) (ComponentInfo, bool) {
	program, component, ok := s.component(componentID)
	if !ok {
		return ComponentInfo{}, false
	}
	_, story := s.storyOf(program, component.ID)
	return ComponentInfo{Name: component.Name, File: component.File, Document: program.Snapshot().DocumentComponent(component.ID), Story: story}, true
}

// CallTarget returns the component the call frame "<callerID>#<site>"
// renders.
func (s *Server) CallTarget(callerID, site string) (string, bool) {
	program := s.registry().Program()
	if program == nil {
		return "", false
	}
	return program.CallTarget(callerID, site)
}

// storyOf returns the key of a component's story: its default story, else
// its first.
func (s *Server) storyOf(program *interp.Program, id compiler.SymbolID) (string, bool) {
	var first string
	for _, story := range program.Stories() {
		if story.Component != id {
			continue
		}
		if story.Default {
			return s.storyKey(story), true
		}
		if first == "" {
			first = s.storyKey(story)
		}
	}
	return first, first != ""
}

// EnsureStory returns the key of a component's story, writing one first
// when it has none. A written story holds values derived from the
// component's input types only, as `ktml stories generate` synthesizes them,
// under the generator's marker; it never holds a rendered page's values. The
// story is type-checked and rendered before it is written, and swapped into
// the program before EnsureStory returns.
func (s *Server) EnsureStory(ctx context.Context, componentID string) (key string, created bool, err error) {
	s.ensure.Lock()
	defer s.ensure.Unlock()
	program, component, ok := s.component(componentID)
	if !ok {
		return "", false, fmt.Errorf("explorer: no component %s in the workspace", componentID)
	}
	if key, ok := s.storyOf(program, component.ID); ok {
		return key, false, nil
	}
	if compiler.IsStoryPath(component.File) {
		return "", false, fmt.Errorf("explorer: %s is declared in a story Document", component.Name)
	}
	result, err := storygen.Plan(s.root, program.Snapshot(), storygen.Options{Components: []compiler.Component{component}, Accumulate: true})
	if err != nil {
		return "", false, fmt.Errorf("explorer: generate a story for %s: %w", component.Name, err)
	}
	if len(result.Skipped) > 0 {
		return "", false, fmt.Errorf("explorer: no story can be generated for %s: %s", component.Name, result.Skipped[0].Reason)
	}
	if err := result.Write(); err != nil {
		return "", false, err
	}
	var written []string
	for _, file := range result.Files {
		if file.Changed {
			written = append(written, file.Path)
		}
	}
	s.Apply(ctx, written)
	program, component, ok = s.component(componentID)
	if !ok {
		return "", false, errors.New("explorer: the component left the workspace")
	}
	key, ok = s.storyOf(program, component.ID)
	if !ok {
		return "", false, fmt.Errorf("explorer: the story of %s was written to %s but does not load; see the Explorer's diagnostics", component.Name, storygen.StoryPath(component.File))
	}
	return key, true, nil
}

// serveOpen opens a component the picker names at its story: GET
// /_explorer/open?component=<import path>.<Name>. It only reads; the
// picker asks ktml dev to create a missing story first.
func (s *Server) serveOpen(w http.ResponseWriter, r *http.Request) {
	componentID := r.URL.Query().Get("component")
	program, component, ok := s.component(componentID)
	if ok {
		if key, ok := s.storyOf(program, component.ID); ok {
			http.Redirect(w, r, "/?"+url.Values{"story": {key}}.Encode(), http.StatusSeeOther)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	message := "No component " + componentID + " is in the workspace."
	if ok {
		message = component.Name + " has no story yet. Pick it again in the app to create one, or run ktml stories generate " + storygen.StoryPath(component.File) + "."
	}
	message = strings.TrimSpace(message)
	_, _ = fmt.Fprintf(w, "<!doctype html><title>KTML Explorer</title><p>%s</p><p><a href=\"/\">All stories</a></p>\n", html.EscapeString(message))
}
