package templmigrate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// bodyOutput marks an output span whose lines are relative to its file's
// component bodies, before document assembly fixes the file and offset.
const bodyOutput = "\x00body"

// schemaDoc is the Host Schema document of an output package: one per
// directory, so records of one Go package never import each other.
func (c *conversion) schemaDoc(dir string) string {
	return filepath.Join(dir, filepath.Base(dir)+".ktmlschema")
}

// units groups each directory's files into KTML documents. Go lets the
// files of a package call each other in a cycle, and KTML forbids import
// cycles, so files that call each other share one document; every other
// file has its own.
func (c *conversion) units() [][]*fileEmitter {
	var files []*fileEmitter
	for _, fe := range c.files {
		files = append(files, fe)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].file.rel < files[j].file.rel })
	index := map[*sourceFile]int{}
	for i, fe := range files {
		index[fe.file] = i
	}
	// Tarjan's strongly connected components over same-directory calls.
	var (
		order, low = make([]int, len(files)), make([]int, len(files))
		onStack    = make([]bool, len(files))
		stack      []int
		counter    = 1
		units      [][]*fileEmitter
		visit      func(int)
	)
	visit = func(v int) {
		order[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for dep := range files[v].componentDeps {
			w, ok := index[dep]
			if !ok || files[w].dir != files[v].dir {
				continue
			}
			if order[w] == 0 {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], order[w])
			}
		}
		if low[v] == order[v] {
			var unit []*fileEmitter
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				unit = append(unit, files[w])
				if w == v {
					break
				}
			}
			sort.Slice(unit, func(i, j int) bool { return unit[i].file.rel < unit[j].file.rel })
			units = append(units, unit)
		}
	}
	for v := range files {
		if order[v] == 0 {
			visit(v)
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i][0].file.rel < units[j][0].file.rel })
	return units
}

// documents renders every KTML document and Host Schema document, and
// fixes the output spans of their nodes.
func (c *conversion) documents() []emittedFile {
	var outputs []emittedFile
	docOf := map[*sourceFile]string{}
	units := c.units()
	for _, unit := range units {
		for _, fe := range unit {
			docOf[fe.file] = unit[0].base + ".ktml"
		}
	}
	for _, unit := range units {
		doc := unit[0].base + ".ktml"
		var k strings.Builder
		sources := make([]string, len(unit))
		for i, fe := range unit {
			sources[i] = fe.file.rel
		}
		fmt.Fprintf(&k, "// Converted from %s by ktml migrate templ (templ %s).\n", strings.Join(sources, ", "), TemplVersion)
		lines := 1
		if len(unit) > 1 {
			k.WriteString("// These files call each other, and KTML forbids import cycles, so they\n// share one document.\n")
			lines += 2
			for _, fe := range unit {
				fe.diagnose(c.node(fe.file, "Package", fe.file.parsed.Package.Expression.Range), fe.file.parsed.Package.Expression.Range, "review", "merged-document",
					fmt.Sprintf("%s calls back into this file; KTML forbids import cycles, so their components share %s", strings.Join(others(sources, fe.file.rel), ", "), c.rel(doc)))
			}
		}
		var records []string
		components := map[string]map[string]bool{}
		for _, fe := range unit {
			for _, r := range fe.records {
				records = append(records, r.name)
			}
			for target, names := range fe.foreignDeps {
				rel := relativeDoc(filepath.Dir(doc), target)
				if components[rel] == nil {
					components[rel] = map[string]bool{}
				}
				for name := range names {
					components[rel][name] = true
				}
			}
			for dep, names := range fe.componentDeps {
				target := docOf[dep]
				if target == doc {
					continue
				}
				rel := relativeDoc(filepath.Dir(doc), target)
				if components[rel] == nil {
					components[rel] = map[string]bool{}
				}
				for name := range names {
					components[rel][name] = true
				}
			}
		}
		if len(records) > 0 {
			fmt.Fprintf(&k, "import { %s } from %q\n", strings.Join(records, ", "), relativeDoc(filepath.Dir(doc), c.schemaDoc(unit[0].dir)))
			lines++
		}
		for _, rel := range sortedMapKeys(components) {
			fmt.Fprintf(&k, "import { %s } from %q\n", strings.Join(sortedSet(components[rel]), ", "), rel)
			lines++
		}
		k.WriteString("\n")
		lines++
		for _, fe := range unit {
			offset := lines
			body := fe.ktml.b.String()
			k.WriteString(body)
			lines += strings.Count(body, "\n")
			for i := range fe.w.nodes {
				n := &fe.w.nodes[i]
				if n.File != fe.file.rel {
					continue
				}
				switch {
				case n.Kind == "Package":
					n.Output = &OutputSpan{File: c.rel(doc), From: 1, To: 1}
				case n.Output != nil && n.Output.File == bodyOutput:
					n.Output = &OutputSpan{File: c.rel(doc), From: n.Output.From + offset - 1, To: n.Output.To + offset - 1}
				}
			}
			fe.srcmap.KTML = c.rel(doc)
		}
		outputs = append(outputs, emittedFile{abs: doc, kind: "ktml", content: []byte(strings.TrimRight(k.String(), "\n") + "\n"), source: strings.Join(sources, ", ")})
	}

	byDir := map[string][]*fileEmitter{}
	for _, unit := range units {
		for _, fe := range unit {
			byDir[fe.dir] = append(byDir[fe.dir], fe)
		}
	}
	for _, dir := range sortedMapKeys(byDir) {
		files := byDir[dir]
		sort.Slice(files, func(i, j int) bool { return files[i].file.rel < files[j].file.rel })
		imports := map[string]map[string]bool{}
		var sources []string
		var body strings.Builder
		for _, fe := range files {
			if len(fe.records) == 0 {
				continue
			}
			sources = append(sources, fe.file.rel)
			for doc, names := range fe.schemaImports {
				rel := relativeDoc(dir, doc)
				if imports[rel] == nil {
					imports[rel] = map[string]bool{}
				}
				for name := range names {
					imports[rel][name] = true
				}
			}
			for _, r := range fe.records {
				body.WriteString("\n")
				if r.comment != "" {
					body.WriteString("// " + r.comment + "\n")
				}
				if r.variants != nil {
					fmt.Fprintf(&body, "state %s {\n", r.name)
					for _, v := range r.variants {
						fmt.Fprintf(&body, "\t%s,\n", v)
					}
					body.WriteString("}\n")
					continue
				}
				if len(r.fields) == 0 {
					fmt.Fprintf(&body, "record %s []\n", r.name)
					continue
				}
				fmt.Fprintf(&body, "record %s [\n", r.name)
				for _, f := range r.fields {
					fmt.Fprintf(&body, "\t%s: %s,\n", f.name, f.ktmlType)
				}
				body.WriteString("]\n")
			}
		}
		if len(sources) == 0 {
			continue
		}
		doc := c.schemaDoc(dir)
		for _, fe := range files {
			if len(fe.records) > 0 {
				fe.srcmap.Schema = c.rel(doc)
			}
		}
		var s strings.Builder
		fmt.Fprintf(&s, "// Host Schema converted from %s by ktml migrate templ.\n", strings.Join(sources, ", "))
		for _, rel := range sortedMapKeys(imports) {
			fmt.Fprintf(&s, "import { %s } from %q\n", strings.Join(sortedSet(imports[rel]), ", "), rel)
		}
		s.WriteString(body.String())
		outputs = append(outputs, emittedFile{abs: doc, kind: "ktmlschema", content: []byte(s.String()), source: strings.Join(sources, ", ")})
	}
	return outputs
}

func others(all []string, self string) []string {
	var rest []string
	for _, s := range all {
		if s != self {
			rest = append(rest, s)
		}
	}
	return rest
}

// relativeDoc spells an import path from a directory to a document.
func relativeDoc(fromDir, target string) string {
	rel, err := filepath.Rel(fromDir, target)
	if err != nil {
		return target
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return rel
}
