package explorer

import (
	"path/filepath"

	"github.com/karl-tye/ktml/internal/compiler"
)

// Location is where the component picker's selection is declared. File is
// relative to the project, with forward slashes; Path is absolute. Line and
// Column are 1-based, Column in UTF-16 code units. Component names the component
// whose source declares it. A zero Location is unknown.
type Location struct {
	File      string `json:"file"`
	Path      string `json:"-"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Component string `json:"component"`
}

// Link is the call that rendered a component instance: the calling
// component's Go ID and the call's c<n> site.
type Link struct {
	Caller string
	Site   string
}

// Locate returns the declaration of every element of path, from the top of
// an instance of the component componentID names. links[0] is the call
// that rendered the instance, links[1] the call that rendered its caller's
// instance, and so on; content at an outlet is located in the call's
// content (compiler.Snapshot.LocateElement).
func (s *Server) Locate(componentID string, links []Link, path []compiler.ElementStep) []Location {
	locations := make([]Location, len(path))
	program, component, ok := s.component(componentID)
	if !ok {
		return locations
	}
	var symbolic []compiler.ContentLink
	for _, link := range links {
		caller, ok := program.Symbol(link.Caller)
		if !ok {
			break
		}
		symbolic = append(symbolic, compiler.ContentLink{Caller: caller, Site: link.Site})
	}
	snapshot := program.Snapshot()
	for index, source := range snapshot.LocateElement(component.ID, symbolic, path) {
		if !source.Found {
			continue
		}
		declaring, _ := snapshot.Component(source.Component)
		locations[index] = s.pickerLocation(source.Range.File, source.Line, source.Column, declaring.Name)
	}
	return locations
}

// Declaration returns where a component is declared.
func (s *Server) Declaration(componentID string) (Location, bool) {
	program, component, ok := s.component(componentID)
	if !ok {
		return Location{}, false
	}
	line, column := position(program.Snapshot().Source(component.File), component.Range.Start)
	return s.pickerLocation(component.File, line, column, component.Name), true
}

func (s *Server) pickerLocation(file string, line, column int, component string) Location {
	relative, err := filepath.Rel(s.root, file)
	if err != nil {
		relative = file
	}
	return Location{File: filepath.ToSlash(relative), Path: file, Line: line, Column: column, Component: component}
}
