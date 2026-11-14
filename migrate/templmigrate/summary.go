package templmigrate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Generated summary markers. WriteSummary replaces only the text between
// them when the file already contains both.
const (
	SummaryBegin = "<!-- BEGIN GENERATED: ktml migrate templ --summary -->"
	SummaryEnd   = "<!-- END GENERATED -->"
)

// WriteSummary writes the human-readable inventory summary.
func WriteSummary(file string, r *Report, categories *Categories) error {
	generated := Summary(r, categories)
	existing, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	begin := bytes.Index(existing, []byte(SummaryBegin))
	end := bytes.Index(existing, []byte(SummaryEnd))
	if begin < 0 || end < begin {
		return os.WriteFile(file, []byte(SummaryBegin+"\n"+generated+SummaryEnd+"\n"), 0o644)
	}
	var out bytes.Buffer
	out.Write(existing[:begin])
	out.WriteString(SummaryBegin + "\n" + generated)
	out.Write(existing[end:])
	return os.WriteFile(file, out.Bytes(), 0o644)
}

// Summary renders the generated Markdown tables.
func Summary(r *Report, categories *Categories) string {
	var b strings.Builder
	t := r.Totals
	percent := func(n int) string {
		if t.Total == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(t.Total))
	}
	fmt.Fprintf(&b, "\n### Totals\n\n")
	fmt.Fprintf(&b, "templ %s parser/v2 AST. %d HTML files with %d templ declarations; %d files excluded (listed below).\n\n", r.TemplVersion, t.Files, t.Templates, t.ExcludedFiles)
	b.WriteString("| Status | Nodes | Share |\n| --- | ---: | ---: |\n")
	for _, status := range Statuses {
		fmt.Fprintf(&b, "| `%s` | %d | %s |\n", status, t.Of(status), percent(t.Of(status)))
	}
	fmt.Fprintf(&b, "| **total** | **%d** | |\n\n", t.Total)
	fmt.Fprintf(&b, "- Dropped nodes: %d. Parse failures: %d. Files classified without go/types: %d.\n", r.Dropped, t.ParseFailures, t.UntypedFiles)
	fmt.Fprintf(&b, "- HTML files with at least one blocked node: %d of %d. Files with none: %d.\n\n", t.FilesBlocked, t.Files, t.FilesConvertible)

	b.WriteString("### By templ node kind\n\n")
	countsTable(&b, "Kind", r.ByKind, nil)
	b.WriteString("### By construct\n\n")
	countsTable(&b, "Construct", r.ByConstruct, nil)
	b.WriteString("### By Phase 7 category\n\n")
	var order []string
	if categories != nil {
		order = categories.Order
	}
	categoryFiles := make(map[string]int)
	for _, file := range r.Files {
		if file.Excluded == "" {
			categoryFiles[file.Category]++
		}
	}
	b.WriteString("| Category | Files | Files without blocked nodes | converted | presentation_scaffolded | review_required | blocked | total |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range orderedKeys(r.ByCategory, order) {
		c := r.ByCategory[name]
		clean := 0
		for _, file := range r.Files {
			if file.Excluded == "" && file.Category == name && len(file.Gaps) == 0 && file.ParseError == "" {
				clean++
			}
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d |\n", name, categoryFiles[name], clean, c.Converted, c.PresentationScaffolded, c.ReviewRequired, c.Blocked, c.Total)
	}
	b.WriteString("\n")

	b.WriteString("### Dependency-ordered migration waves\n\n")
	b.WriteString("A KTML component cannot call a templ component, so a file can convert only after every templ file it calls. Wave 1 files have no blocked nodes and call no other templ file. Wave n files call only files in earlier waves. Files in no wave are blocked themselves, or call a file that is.\n\n")
	waves := make(map[int][]FileReport)
	maxWave := 0
	for _, file := range r.Files {
		if file.Excluded == "" {
			waves[file.Wave] = append(waves[file.Wave], file)
			maxWave = max(maxWave, file.Wave)
		}
	}
	b.WriteString("| Wave | Files | Nodes | Categories |\n| --- | ---: | ---: | --- |\n")
	for wave := 1; wave <= maxWave+1; wave++ {
		key := wave
		label := fmt.Sprint(wave)
		if wave == maxWave+1 {
			key, label = 0, "none"
		}
		nodes := 0
		categoryCount := make(map[string]int)
		for _, file := range waves[key] {
			nodes += file.Counts.Total
			categoryCount[file.Category]++
		}
		var parts []string
		for _, name := range orderedKeys(countsOf(categoryCount), order) {
			parts = append(parts, fmt.Sprintf("%s %d", name, categoryCount[name]))
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s |\n", label, len(waves[key]), nodes, strings.Join(parts, ", "))
	}
	b.WriteString("\n")

	b.WriteString("### Top blockers\n\n")
	b.WriteString("Each blocked node names the missing KTML feature (gap) that blocks it. \"Sole blocker\" counts files that this gap alone keeps blocked.\n\n")
	b.WriteString("| Gap | Blocked nodes | Files | Sole blocker in |\n| --- | ---: | ---: | ---: |\n")
	for _, gap := range r.Gaps {
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d |\n", gap.Name, gap.Nodes, len(gap.Files), len(gap.SoleBlocker))
	}
	b.WriteString("\n### Unblock order\n\n")
	b.WriteString("Greedy order: each step closes the gap that puts the most further HTML files into a migration wave (dependency-ready), with ties broken by blocked nodes. A file stays out of every wave while it, or any file it calls, has a blocked node.\n\n")
	b.WriteString("| Step | Gap | Nodes unblocked | Files newly ready | Files ready (cumulative) |\n| ---: | --- | ---: | ---: | ---: |\n")
	for i, step := range unblockOrder(r) {
		fmt.Fprintf(&b, "| %d | `%s` | %d | %d | %d |\n", i+1, step.gap, step.nodes, step.newFiles, step.cumulative)
	}
	b.WriteString("\n")

	for _, status := range []Status{ReviewRequired, PresentationScaffolded} {
		fmt.Fprintf(&b, "### `%s` reasons\n\n", status)
		b.WriteString("| Reason | Nodes |\n| --- | ---: |\n")
		reasons := r.Reasons[status.String()]
		keys := sortedKeys(reasons)
		sort.SliceStable(keys, func(i, j int) bool { return reasons[keys[i]] > reasons[keys[j]] })
		for _, key := range keys {
			fmt.Fprintf(&b, "| `%s` | %d |\n", key, reasons[key])
		}
		b.WriteString("\n")
	}

	s := r.Scaffold
	b.WriteString("### Presenter scaffold volume\n\n")
	fmt.Fprintf(&b, "- Templates: %d, of which %d need a presenter (any derived field, state, or record input).\n", s.Templates, s.TemplatesNeedingPresenter)
	fmt.Fprintf(&b, "- Derived presenter fields (scaffolded expressions and local initializations): %d.\n", s.DerivedFields)
	fmt.Fprintf(&b, "- Closed Presentation States (switch statements): %d.\n", s.States)
	fmt.Fprintf(&b, "- Distinct Go types used as record inputs (Host Schema record candidates): %d.\n\n", len(s.RecordTypes))

	b.WriteString("### Per file\n\n")
	b.WriteString("| File | Category | Wave | Templates | converted | scaffolded | review | blocked | Gaps |\n| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n")
	for _, file := range r.Files {
		if file.Excluded != "" {
			continue
		}
		gaps := strings.Join(file.Gaps, ", ")
		if file.ParseError != "" {
			gaps = "parse error"
		}
		c := file.Counts
		wave := "–"
		if file.Wave > 0 {
			wave = fmt.Sprint(file.Wave)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d | %d | %d | %d | %d | %s |\n", file.Path, file.Category, wave, file.Templates, c.Converted, c.PresentationScaffolded, c.ReviewRequired, c.Blocked, gaps)
	}
	b.WriteString("\n### Excluded files\n\n")
	if t.ExcludedFiles == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("Excluded files are classified (every node `review_required`) but kept out of the totals above.\n\n")
		b.WriteString("| File | Reason | Templates | Nodes |\n| --- | --- | ---: | ---: |\n")
		for _, file := range r.Files {
			if file.Excluded != "" {
				fmt.Fprintf(&b, "| `%s` | %s | %d | %d |\n", file.Path, file.Excluded, file.Templates, file.Counts.Total)
			}
		}
	}
	b.WriteString("\n")
	if len(r.TypeProblems) > 0 {
		b.WriteString("### Type-loading problems\n\n")
		for _, problem := range r.TypeProblems {
			fmt.Fprintf(&b, "- %s\n", problem)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func countsTable(b *strings.Builder, label string, counts map[string]Counts, order []string) {
	fmt.Fprintf(b, "| %s | converted | presentation_scaffolded | review_required | blocked | total |\n| --- | ---: | ---: | ---: | ---: | ---: |\n", label)
	keys := orderedKeys(counts, order)
	if order == nil {
		sort.SliceStable(keys, func(i, j int) bool { return counts[keys[i]].Total > counts[keys[j]].Total })
	}
	for _, key := range keys {
		c := counts[key]
		fmt.Fprintf(b, "| `%s` | %d | %d | %d | %d | %d |\n", key, c.Converted, c.PresentationScaffolded, c.ReviewRequired, c.Blocked, c.Total)
	}
	b.WriteString("\n")
}

func orderedKeys(counts map[string]Counts, order []string) []string {
	var keys []string
	seen := make(map[string]bool)
	for _, key := range order {
		if _, ok := counts[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	for _, key := range sortedKeys(counts) {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	return keys
}

type unblockStep struct {
	gap                         string
	nodes, newFiles, cumulative int
}

// unblockOrder greedily closes the gap that makes the most further HTML
// files dependency-ready (in some migration wave), breaking ties by blocked
// nodes.
func unblockOrder(r *Report) []unblockStep {
	nodes := make(map[string]int)
	for _, gap := range r.Gaps {
		nodes[gap.Name] = gap.Nodes
	}
	closed := make(map[string]bool)
	cumulative := len(r.waves(closed))
	var steps []unblockStep
	for len(nodes) > 0 {
		best, bestReady := "", -1
		for _, gap := range sortedKeys(nodes) {
			closed[gap] = true
			ready := len(r.waves(closed))
			delete(closed, gap)
			if ready > bestReady || ready == bestReady && nodes[gap] > nodes[best] {
				best, bestReady = gap, ready
			}
		}
		closed[best] = true
		steps = append(steps, unblockStep{gap: best, nodes: nodes[best], newFiles: bestReady - cumulative, cumulative: bestReady})
		cumulative = bestReady
		delete(nodes, best)
	}
	return steps
}

func countsOf(m map[string]int) map[string]Counts {
	counts := make(map[string]Counts, len(m))
	for key, n := range m {
		counts[key] = Counts{Total: n}
	}
	return counts
}
