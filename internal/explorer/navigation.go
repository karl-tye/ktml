package explorer

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/reach"
)

// The navigation groups stories by kind, then package (the component
// Document's directory), then component. Pages are components a route's
// handler renders; Components are reached only through KTML calls; Other
// are the rest. The kind comes from the reachability `ktml stories
// generate` computes (internal/reach), which the Explorer computes in the
// background at start and after a component change. Until it is ready, or
// when it fails (no Go module, no routes), a story's generated header names
// the kind and routes; a story without one is Other.

// kinds in navigation order.
var kinds = []string{"Pages", "Components", "Other"}

const (
	kindPage = iota
	kindComponent
	kindOther
)

// navigation is the grouped story tree of the interface.
type navigation struct {
	Kinds  []navKind
	Routes []string
}

type navKind struct {
	Name     string
	Packages []*navPackage
}

type navPackage struct {
	Name       string
	Components []*navComponent
}

type navComponent struct {
	Name    string
	File    string
	Routes  []int
	Open    bool
	Stories []storyLink
}

type storyLink struct {
	Name     string
	Key      string
	Selected bool
}

func (k navKind) count() int {
	count := 0
	for _, pkg := range k.Packages {
		count += pkg.count()
	}
	return count
}

func (p *navPackage) count() int {
	count := 0
	for _, component := range p.Components {
		count += len(component.Stories)
	}
	return count
}

func (k navKind) open() bool {
	for _, pkg := range k.Packages {
		if pkg.open() {
			return true
		}
	}
	return false
}

func (p *navPackage) open() bool {
	for _, component := range p.Components {
		if component.Open {
			return true
		}
	}
	return false
}

// reachability is the last reachability analysis: its result, or nil when
// it failed.
type reachability struct {
	result *reach.Result
}

// analyze computes reachability for the current workspace.
func (s *Server) analyze() {
	s.mu.Lock()
	snapshot := s.workspace.Snapshot()
	s.mu.Unlock()
	started := time.Now()
	result, err := s.reachability(snapshot)
	if err == nil && !s.reachLogged {
		s.reachLogged = true
		s.options.Logf("ktml explore: stories grouped by reachability: %d routes, %d pages, %d components (%v)", len(result.Routes), result.Pages(), len(result.Reachable)-result.Pages(), time.Since(started).Round(time.Millisecond))
	}
	if err != nil {
		if message := err.Error(); message != s.reachError {
			s.reachError = message
			s.options.Logf("ktml explore: reachability: %v; stories are grouped by their generated headers", err)
		}
		result = nil
	} else {
		s.reachError = ""
	}
	s.reach.Store(&reachability{result: result})
}

func (s *Server) reachability(snapshot *compiler.Snapshot) (*reach.Result, error) {
	chi, err := reach.ChiSettings(s.root, "")
	if err != nil {
		return nil, err
	}
	return reach.Analyze(reach.Options{Root: s.root, Chi: chi, Snapshot: snapshot})
}

// analyzeLoop computes reachability at start and again after each component
// change, one analysis at a time, and refreshes the interfaces after each.
func (s *Server) analyzeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.reanalyze:
		}
		s.analyze()
		s.notify()
	}
}

// requestAnalysis asks the analysis loop for a new analysis; requests made
// while one runs coalesce.
func (s *Server) requestAnalysis() {
	select {
	case s.reanalyze <- struct{}{}:
	default:
	}
}

// header is a component's kind and routes as a generated story header
// names them.
type header struct {
	kind   int
	routes []string
}

var (
	headerPattern = regexp.MustCompile(`^// (\S+) is (a page|a component rendered through KTML calls|not reached by any route)`)
	routePattern  = regexp.MustCompile(`^//   ([A-Z]+ /.*)$`)
)

// headers reads the generated headers of a story Document by component name.
func headers(source string) map[string]header {
	result := make(map[string]header)
	var current string
	for line := range strings.SplitSeq(source, "\n") {
		line = strings.TrimRight(line, "\r")
		if match := headerPattern.FindStringSubmatch(line); match != nil {
			current = match[1]
			kind := kindOther
			switch match[2] {
			case "a page":
				kind = kindPage
			case "a component rendered through KTML calls":
				kind = kindComponent
			}
			if _, seen := result[current]; !seen {
				result[current] = header{kind: kind}
			}
			continue
		}
		if match := routePattern.FindStringSubmatch(line); match != nil && current != "" {
			found := result[current]
			found.routes = append(found.routes, match[1])
			result[current] = found
			continue
		}
		current = ""
	}
	return result
}

// navigation groups the program's stories, with the selected story's path
// open.
func (s *Server) navigation(program *interp.Program, selected string) navigation {
	if program == nil {
		return navigation{}
	}
	var result *reach.Result
	if state := s.reach.Load(); state != nil {
		result = state.result
	}
	snapshot := program.Snapshot()
	type entry struct {
		kind      int
		dir       string
		component *navComponent
		routes    []string
	}
	var entries []*entry
	byComponent := make(map[compiler.SymbolID]*entry)
	fileHeaders := make(map[string]map[string]header)
	for _, story := range program.Stories() {
		found, ok := byComponent[story.Component]
		if !ok {
			file := story.File
			if component, declared := snapshot.Component(story.Component); declared {
				file = component.File
			}
			dir, _ := filepath.Rel(s.root, filepath.Dir(file))
			found = &entry{kind: kindOther, dir: filepath.ToSlash(dir)}
			relative, _ := filepath.Rel(s.root, story.File)
			found.component = &navComponent{Name: story.ComponentName, File: filepath.ToSlash(relative)}
			if reached, ok := result.Lookup(story.Component); ok {
				found.kind = kindComponent
				if reached.Page {
					found.kind = kindPage
				}
				for _, route := range reached.Routes {
					found.routes = append(found.routes, route.String()+" → "+route.Handler)
				}
			} else if result == nil {
				parsed, read := fileHeaders[story.File]
				if !read {
					parsed = headers(string(snapshot.Source(story.File)))
					fileHeaders[story.File] = parsed
				}
				if named, ok := parsed[story.ComponentName]; ok {
					found.kind, found.routes = named.kind, named.routes
				}
			}
			byComponent[story.Component] = found
			entries = append(entries, found)
		}
		key := s.storyKey(story)
		found.component.Stories = append(found.component.Stories, storyLink{Name: story.Name, Key: key, Selected: key == selected})
		found.component.Open = found.component.Open || key == selected
	}

	dirs := make([]string, len(entries))
	for i, found := range entries {
		dirs[i] = found.dir
	}
	names := packageNames(dirs)
	routeSet := make(map[string]bool)
	for _, found := range entries {
		for _, route := range found.routes {
			routeSet[route] = true
		}
	}
	nav := navigation{Routes: make([]string, 0, len(routeSet))}
	for route := range routeSet {
		nav.Routes = append(nav.Routes, route)
	}
	sort.Strings(nav.Routes)
	routeIndex := make(map[string]int, len(nav.Routes))
	for i, route := range nav.Routes {
		routeIndex[route] = i
	}

	packages := make([]map[string]*navPackage, len(kinds))
	for i := range packages {
		packages[i] = make(map[string]*navPackage)
	}
	for i, found := range entries {
		for _, route := range found.routes {
			found.component.Routes = append(found.component.Routes, routeIndex[route])
		}
		sort.Ints(found.component.Routes)
		pkg, ok := packages[found.kind][names[i]]
		if !ok {
			pkg = &navPackage{Name: names[i]}
			packages[found.kind][names[i]] = pkg
		}
		pkg.Components = append(pkg.Components, found.component)
	}
	for i, name := range kinds {
		kind := navKind{Name: name}
		for _, pkg := range packages[i] {
			sort.SliceStable(pkg.Components, func(a, b int) bool {
				if pkg.Components[a].Name != pkg.Components[b].Name {
					return pkg.Components[a].Name < pkg.Components[b].Name
				}
				return pkg.Components[a].File < pkg.Components[b].File
			})
			kind.Packages = append(kind.Packages, pkg)
		}
		sort.Slice(kind.Packages, func(a, b int) bool { return kind.Packages[a].Name < kind.Packages[b].Name })
		if len(kind.Packages) > 0 {
			nav.Kinds = append(nav.Kinds, kind)
		}
	}
	return nav
}

// packageNames names package directories, relative to the root, without
// the parent directories they all share (internal/web becomes web when
// every package is under internal), unless that parent is a package.
func packageNames(dirs []string) []string {
	var common []string
	for i, dir := range dirs {
		parts := strings.Split(dir, "/")
		if i == 0 {
			common = parts
			continue
		}
		n := 0
		for n < len(common) && n < len(parts) && common[n] == parts[n] {
			n++
		}
		common = common[:n]
	}
	if len(common) == 1 && common[0] == "." {
		common = nil
	}
	for len(common) > 0 {
		prefix := strings.Join(common, "/")
		isPackage := false
		for _, dir := range dirs {
			if dir == prefix {
				isPackage = true
				break
			}
		}
		if !isPackage {
			break
		}
		common = common[:len(common)-1]
	}
	names := make([]string, len(dirs))
	prefix := strings.Join(common, "/")
	for i, dir := range dirs {
		if prefix == "" {
			names[i] = dir
		} else {
			names[i] = strings.TrimPrefix(dir, prefix+"/")
		}
	}
	return names
}

// renderNavigation is the story tree: collapsed <details> groups, the
// selected story's path open, and the route index search matches against.
func renderNavigation(nav navigation) string {
	var b strings.Builder
	b.WriteString(`<div class="tree" data-tree>`)
	for _, kind := range nav.Kinds {
		id := kind.Name
		openGroup(&b, "kind", id, kind.Name, kind.count(), kind.open(), "")
		for _, pkg := range kind.Packages {
			packageID := id + ":" + pkg.Name
			openGroup(&b, "package", packageID, pkg.Name, pkg.count(), pkg.open(), "")
			for _, component := range pkg.Components {
				routes := make([]string, len(component.Routes))
				for i, route := range component.Routes {
					routes[i] = strconv.Itoa(route)
				}
				extra := ` title="` + attr(component.File) + `"`
				if len(routes) > 0 {
					extra = ` data-routes="` + strings.Join(routes, " ") + `"` + extra
				}
				openGroup(&b, "component", packageID+":"+component.Name, component.Name, -1, component.Open, extra)
				b.WriteString(`<ul>`)
				for _, story := range component.Stories {
					current := ""
					if story.Selected {
						current = ` aria-current="page"`
					}
					b.WriteString(`<li data-story-item><a href="/?story=` + attr(urlQuery(story.Key)) + `"` + current + ` data-label>` + text(story.Name) + `</a></li>`)
				}
				b.WriteString(`</ul></details>`)
			}
			b.WriteString(`</details>`)
		}
		b.WriteString(`</details>`)
	}
	b.WriteString(`</div>`)
	routes, _ := json.Marshal(nav.Routes)
	// encoding/json escapes <, >, and &, so the JSON is safe in a script.
	b.WriteString(`<script type="application/json" data-route-index>` + string(routes) + `</script>`)
	return b.String()
}

// openGroup writes a group's <details> and <summary>; count < 0 shows none.
func openGroup(b *strings.Builder, class, id, label string, count int, open bool, extra string) {
	b.WriteString(`<details class="group ` + class + `" data-group="` + attr(id) + `"`)
	if count >= 0 {
		b.WriteString(` data-count="` + strconv.Itoa(count) + `"`)
	}
	b.WriteString(extra)
	if open {
		// data-current keeps the selected story's path open after the
		// viewer's remembered groups are applied.
		b.WriteString(` data-current open`)
	}
	b.WriteString(`><summary><span data-label>` + text(label) + `</span>`)
	if count >= 0 {
		b.WriteString(` <span class="count">` + strconv.Itoa(count) + `</span>`)
	}
	if class == "component" {
		b.WriteString(`<span class="route" data-route hidden></span>`)
	}
	b.WriteString(`</summary>`)
}
