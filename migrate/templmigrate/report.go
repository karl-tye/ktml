package templmigrate

import (
	"encoding/json"
	"os"
	"sort"
)

// Report is the machine-readable inventory.
type Report struct {
	Schema       string `json:"schema"`
	TemplVersion string `json:"templVersion"`
	// Totals cover included (HTML) files; excluded files are counted apart.
	Totals Totals `json:"totals"`
	// Excluded counts the nodes of excluded files.
	Excluded     Counts                    `json:"excluded"`
	ByKind       map[string]Counts         `json:"byKind"`
	ByConstruct  map[string]Counts         `json:"byConstruct"`
	ByCategory   map[string]Counts         `json:"byCategory"`
	Reasons      map[string]map[string]int `json:"reasons"`
	Gaps         []Gap                     `json:"gaps"`
	Files        []FileReport              `json:"files"`
	Presenters   []PresenterNeed           `json:"presenters"`
	Scaffold     ScaffoldEstimate          `json:"scaffold"`
	Dropped      int                       `json:"dropped"`
	TypeProblems []string                  `json:"typeProblems,omitempty"`
	// Conversion summarizes a conversion run's output; nil for --dry-run.
	Conversion *Conversion `json:"conversion,omitempty"`
	// NodesOmitted names the status whose nodes were left out of Nodes.
	NodesOmitted string `json:"nodesOmitted,omitempty"`
	Nodes        []Node `json:"nodes"`
}

// Counts holds node counts per status.
type Counts struct {
	Converted              int `json:"converted"`
	PresentationScaffolded int `json:"presentation_scaffolded"`
	ReviewRequired         int `json:"review_required"`
	Blocked                int `json:"blocked"`
	Total                  int `json:"total"`
}

func (c *Counts) add(status Status) {
	c.Total++
	switch status {
	case Converted:
		c.Converted++
	case PresentationScaffolded:
		c.PresentationScaffolded++
	case ReviewRequired:
		c.ReviewRequired++
	case Blocked:
		c.Blocked++
	}
}

// Of returns the count for one status.
func (c Counts) Of(status Status) int {
	switch status {
	case Converted:
		return c.Converted
	case PresentationScaffolded:
		return c.PresentationScaffolded
	case ReviewRequired:
		return c.ReviewRequired
	case Blocked:
		return c.Blocked
	}
	return 0
}

// Totals summarize the included files.
type Totals struct {
	Counts
	Files            int `json:"files"`
	ExcludedFiles    int `json:"excludedFiles"`
	Templates        int `json:"templates"`
	ParseFailures    int `json:"parseFailures"`
	UntypedFiles     int `json:"untypedFiles"`
	FilesBlocked     int `json:"filesWithBlockedNodes"`
	FilesConvertible int `json:"filesWithoutBlockedNodes"`
}

// FileReport summarizes one templ file.
type FileReport struct {
	Path        string   `json:"path"`
	Package     string   `json:"package,omitempty"`
	Category    string   `json:"category"`
	Excluded    string   `json:"excluded,omitempty"`
	ParseError  string   `json:"parseError,omitempty"`
	Diagnostics []string `json:"diagnostics,omitempty"`
	Typed       bool     `json:"typed"`
	Templates   int      `json:"templates"`
	Counts      Counts   `json:"counts"`
	// Gaps lists the missing KTML features blocking this file.
	Gaps []string `json:"gaps,omitempty"`
	// DependsOn lists the other templ files whose components this file calls.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Wave is the dependency-ordered migration wave in which the file can
	// convert: 1 when it has no blocked nodes and calls no other templ file,
	// n when every file it calls is in an earlier wave, and 0 when it, or a
	// file it depends on, is blocked.
	Wave int `json:"wave"`
}

// Gap is a missing KTML feature and the blocked nodes it accounts for.
type Gap struct {
	Name  string   `json:"name"`
	Nodes int      `json:"nodes"`
	Files []string `json:"files"`
	// SoleBlocker lists the files this gap alone keeps blocked.
	SoleBlocker []string `json:"soleBlocker,omitempty"`
}

// ScaffoldEstimate sizes the presenter and Host Schema work.
type ScaffoldEstimate struct {
	Templates                 int      `json:"templates"`
	TemplatesNeedingPresenter int      `json:"templatesNeedingPresenter"`
	DerivedFields             int      `json:"derivedFields"`
	States                    int      `json:"states"`
	RecordTypes               []string `json:"recordTypes"`
}

// Failed reports whether the inventory must exit nonzero: a node was
// dropped or blocked, or a file did not parse.
func (r *Report) Failed() bool {
	return r.Dropped > 0 || r.Totals.Blocked > 0 || r.Excluded.Blocked > 0 || r.Totals.ParseFailures > 0
}

func (r *Report) summarize() {
	r.ByKind = make(map[string]Counts)
	r.ByConstruct = make(map[string]Counts)
	r.ByCategory = make(map[string]Counts)
	r.Reasons = make(map[string]map[string]int)
	excluded := make(map[string]bool)
	category := make(map[string]string)
	fileIndex := make(map[string]int)
	for i, file := range r.Files {
		fileIndex[file.Path] = i
		category[file.Path] = file.Category
		if file.Excluded != "" {
			excluded[file.Path] = true
			r.Totals.ExcludedFiles++
		} else {
			r.Totals.Files++
			r.Totals.Templates += file.Templates
			if !file.Typed {
				r.Totals.UntypedFiles++
			}
		}
		if file.ParseError != "" {
			r.Totals.ParseFailures++
		}
	}
	gapNodes := make(map[string]int)
	gapFiles := make(map[string]map[string]bool)
	fileGaps := make(map[string]map[string]bool)
	for _, node := range r.Nodes {
		index := fileIndex[node.File]
		counts := r.Files[index].Counts
		counts.add(node.Status)
		r.Files[index].Counts = counts
		if excluded[node.File] {
			r.Excluded.add(node.Status)
			continue
		}
		r.Totals.add(node.Status)
		add(r.ByKind, node.Kind, node.Status)
		add(r.ByConstruct, node.Construct, node.Status)
		add(r.ByCategory, category[node.File], node.Status)
		for _, reason := range node.Reasons {
			if r.Reasons[node.Status.String()] == nil {
				r.Reasons[node.Status.String()] = make(map[string]int)
			}
			r.Reasons[node.Status.String()][reason]++
		}
		if node.Status == Blocked {
			gapNodes[node.Gap]++
			if gapFiles[node.Gap] == nil {
				gapFiles[node.Gap] = make(map[string]bool)
			}
			gapFiles[node.Gap][node.File] = true
			if fileGaps[node.File] == nil {
				fileGaps[node.File] = make(map[string]bool)
			}
			fileGaps[node.File][node.Gap] = true
		}
	}
	for i := range r.Files {
		r.Files[i].Gaps = sortedKeys(fileGaps[r.Files[i].Path])
		if r.Files[i].Excluded != "" {
			continue
		}
		if len(r.Files[i].Gaps) > 0 || r.Files[i].ParseError != "" {
			r.Totals.FilesBlocked++
		} else {
			r.Totals.FilesConvertible++
		}
	}
	r.assignWaves()
	for name, nodes := range gapNodes {
		gap := Gap{Name: name, Nodes: nodes, Files: sortedKeys(gapFiles[name])}
		for _, file := range gap.Files {
			if len(fileGaps[file]) == 1 {
				gap.SoleBlocker = append(gap.SoleBlocker, file)
			}
		}
		r.Gaps = append(r.Gaps, gap)
	}
	sort.Slice(r.Gaps, func(i, j int) bool {
		if r.Gaps[i].Nodes != r.Gaps[j].Nodes {
			return r.Gaps[i].Nodes > r.Gaps[j].Nodes
		}
		return r.Gaps[i].Name < r.Gaps[j].Name
	})
	records := make(map[string]bool)
	for _, need := range r.Presenters {
		if need.Excluded {
			continue
		}
		r.Scaffold.Templates++
		if need.Derived > 0 || need.States > 0 || len(need.Records) > 0 {
			r.Scaffold.TemplatesNeedingPresenter++
		}
		r.Scaffold.DerivedFields += need.Derived
		r.Scaffold.States += need.States
		for _, record := range need.Records {
			records[record] = true
		}
	}
	r.Scaffold.RecordTypes = sortedKeys(records)
}

func add(m map[string]Counts, key string, status Status) {
	counts := m[key]
	counts.add(status)
	m[key] = counts
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// WriteJSON writes the report as indented JSON. With allNodes false, the
// node list keeps only nodes that are not converted; every count still
// covers all nodes.
func (r *Report) WriteJSON(file string, allNodes bool) error {
	out := *r
	if !allNodes {
		out.Nodes = nil
		out.NodesOmitted = "converted"
		for _, node := range r.Nodes {
			if node.Status != Converted {
				out.Nodes = append(out.Nodes, node)
			}
		}
	}
	data, err := json.MarshalIndent(&out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}

// assignWaves orders the HTML files for migration: a KTML component cannot
// call a templ one, so a file converts only after every file it calls.
func (r *Report) assignWaves() {
	deps := make(map[string]map[string]bool)
	for _, node := range r.Nodes {
		for _, callee := range append([]string{node.Callee}, node.References...) {
			if callee == "" || callee == node.File {
				continue
			}
			if deps[node.File] == nil {
				deps[node.File] = make(map[string]bool)
			}
			deps[node.File][callee] = true
		}
	}
	for i := range r.Files {
		r.Files[i].DependsOn = sortedKeys(deps[r.Files[i].Path])
	}
	waves := r.waves(nil)
	for i := range r.Files {
		r.Files[i].Wave = waves[r.Files[i].Path]
	}
}

// waves computes each HTML file's migration wave as if the given gaps were
// closed. Files that call each other (a dependency cycle) form one unit that
// migrates together.
func (r *Report) waves(closed map[string]bool) map[string]int {
	blocked := func(file FileReport) bool {
		if file.Excluded != "" || file.ParseError != "" {
			return true
		}
		for _, gap := range file.Gaps {
			if !closed[gap] {
				return true
			}
		}
		return false
	}
	byPath := make(map[string]FileReport)
	for _, file := range r.Files {
		byPath[file.Path] = file
	}
	unit := r.dependencyUnits()
	members := make(map[string][]string)
	for _, file := range r.Files {
		members[unit[file.Path]] = append(members[unit[file.Path]], file.Path)
	}
	wave := make(map[string]int)
	for current := 1; ; current++ {
		var ready []string
		for _, id := range sortedKeys(members) {
			if wave[id] != 0 {
				continue
			}
			ok := true
			for _, path := range members[id] {
				if blocked(byPath[path]) {
					ok = false
					break
				}
				for _, dep := range byPath[path].DependsOn {
					depUnit, known := unit[dep]
					if !known || depUnit != id && wave[depUnit] == 0 {
						ok = false
						break
					}
				}
			}
			if ok {
				ready = append(ready, id)
			}
		}
		if len(ready) == 0 {
			break
		}
		for _, id := range ready {
			wave[id] = current
		}
	}
	files := make(map[string]int)
	for id, n := range wave {
		for _, path := range members[id] {
			files[path] = n
		}
	}
	return files
}

// dependencyUnits maps each file to the first path, in report order, of the
// set of files that reach each other through calls.
func (r *Report) dependencyUnits() map[string]string {
	reach := make(map[string]map[string]bool)
	for _, file := range r.Files {
		seen := map[string]bool{file.Path: true}
		stack := []string{file.Path}
		for len(stack) > 0 {
			path := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, other := range r.Files {
				if other.Path != path {
					continue
				}
				for _, dep := range other.DependsOn {
					if !seen[dep] {
						seen[dep] = true
						stack = append(stack, dep)
					}
				}
			}
		}
		reach[file.Path] = seen
	}
	unit := make(map[string]string)
	for _, file := range r.Files {
		for _, other := range r.Files {
			if reach[file.Path][other.Path] && reach[other.Path][file.Path] {
				unit[file.Path] = other.Path
				break
			}
		}
	}
	return unit
}
