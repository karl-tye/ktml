package storygen

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGenerate_GIVENAGeneratedDocument_WHENRunAgain_THENNothingChanges checks
// that generation is deterministic: a second run, and a run in a fresh copy,
// write the same bytes.
func TestGenerate_GIVENAGeneratedDocument_WHENRunAgain_THENNothingChanges(t *testing.T) {
	first := copyFixture(t, "basic")
	generate(t, first, nil)
	path := filepath.Join(first, "web", "board.story.ktml")
	written := readFile(t, path)

	again := generate(t, first, nil)
	for _, file := range again.Files {
		if file.Changed {
			t.Errorf("%s changed on a second run: %+v", file.Path, file.Report)
		}
		if len(file.Report.Unchanged) == 0 || len(file.Report.Added)+len(file.Report.Updated)+len(file.Report.Removed) > 0 {
			t.Errorf("second run report = %+v, want every story unchanged", file.Report)
		}
	}
	if got := readFile(t, path); got != written {
		t.Errorf("second run rewrote the Document")
	}

	second := copyFixture(t, "basic")
	generate(t, second, nil)
	if got := readFile(t, filepath.Join(second, "web", "board.story.ktml")); got != written {
		t.Errorf("a run in another copy wrote different bytes")
	}
}

// TestGenerate_GIVENEditedAndHandWrittenStories_WHENRunAgain_THENKeepsThem
// edits one generated story, adds a hand-written story under a generated
// name and one under a new name, and removes a component's Variant: the
// rerun keeps the edited and hand-written stories byte for byte, reports
// them, removes the stale generated story, and adds the new one.
func TestGenerate_GIVENEditedAndHandWrittenStories_WHENRunAgain_THENKeepsThem(t *testing.T) {
	root := copyFixture(t, "basic")
	generate(t, root, nil)
	path := filepath.Join(root, "web", "board.story.ktml")
	source := readFile(t, path)

	// Edit StatusLine.Booked's values under its marker.
	edited := strings.Replace(source, `status = Booked [engineerName = "Jordan Brennan", slot = "09:15"],`, `status = Booked [engineerName = "My engineer", slot = "noon"],`, 1)
	if edited == source {
		t.Fatal("the fixture's StatusLine.Booked story changed; update the test")
	}
	// Replace the marked Panel.Default story with a hand-written one: no
	// marker.
	start := strings.Index(edited, "// ktml:generated")
	panel := strings.Index(edited, "story Panel.Default")
	markerStart := strings.LastIndex(edited[:panel], "// ktml:generated")
	end := panel + strings.Index(edited[panel:], "\n}\n") + 3
	if start < 0 || markerStart < 0 {
		t.Fatal("no marker found")
	}
	own := "story Panel.Default [title = \"Mine\"] {\n\tslot actions {\n\t\t\"Act\"\n\t}\n}\n"
	edited = edited[:markerStart] + own + edited[end:]
	edited += "\nstory Panel.Custom [title = \"Custom\"] {\n\tslot actions {\n\t\t\"Act\"\n\t}\n}\n"
	writeFile(t, path, edited)

	// Drop ProgressLine's Failed Variant and add Paused.
	schema := filepath.Join(root, "web", "model.ktmlschema")
	writeFile(t, schema, strings.Replace(readFile(t, schema), "\tFailed [\n\t\tmessage: string,\n\t],\n}\n", "\tPaused,\n}\n", 1))
	board := filepath.Join(root, "web", "board.ktml")
	writeFile(t, board, strings.Replace(readFile(t, board), "ProgressLine [progress = Failed, message] {\n\tp \"Failed: {message}\"\n}", "ProgressLine [progress = Paused] {\n\tp \"Paused\"\n}", 1))

	result := generate(t, root, nil)
	report := result.Files[0].Report
	got := readFile(t, path)
	for _, want := range []string{
		`status = Booked [engineerName = "My engineer", slot = "noon"],`,
		own,
		"story Panel.Custom [",
		"story ProgressLine.Paused",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rerun lost or lacks %q", want)
		}
	}
	if strings.Contains(got, "story ProgressLine.Failed") {
		t.Errorf("rerun kept the stale Failed stories")
	}
	if !slices.Contains(report.Edited, "StatusLine.Booked") {
		t.Errorf("Edited = %v, want StatusLine.Booked", report.Edited)
	}
	if !slices.Contains(report.Own, "Panel.Default") {
		t.Errorf("Own = %v, want Panel.Default", report.Own)
	}
	if !slices.Contains(report.Removed, "ProgressLine.Failed") || !slices.Contains(report.Added, "ProgressLine.Paused") {
		t.Errorf("Removed = %v, Added = %v", report.Removed, report.Added)
	}
	if strings.Count(got, "story Panel.Default") != 1 {
		t.Errorf("Panel.Default is written twice")
	}
}

// TestMerge_GIVENAHandWrittenDocument_WHENMerged_THENAddsBelowItsContent
// merges into a Document a person wrote: its text is kept, imports and
// fixtures it already has are reused, and the generated blocks follow.
func TestMerge_GIVENAHandWrittenDocument_WHENMerged_THENAddsBelowItsContent(t *testing.T) {
	source := "// My stories.\nimport { A } from \"./x.ktml\"\n\nfixture placeholderURL: JSONValue = `1`\n\nstory A.Mine [v = 1]\n"
	desired := Desired{
		Header:   GeneratedHeader,
		Imports:  []Import{{Path: "./x.ktml", Names: []string{"A", "B"}}},
		Fixtures: []FixtureDecl{{Name: "placeholderURL2", Type: "TrustedURL", Value: "/"}},
		Stories:  []Block{{Key: "B.Default", Text: "story B.Default"}, {Key: "A.Mine", Text: "story A.Mine [v = 2]"}},
	}
	merged, report, err := Merge([]byte(source), desired, GeneratedMarker)
	if err != nil {
		t.Fatal(err)
	}
	got := string(merged)
	if !strings.HasPrefix(got, "// My stories.\nimport { A } from \"./x.ktml\"\n") {
		t.Errorf("the hand-written start changed:\n%s", got)
	}
	for _, want := range []string{"import { B } from \"./x.ktml\"", "fixture placeholderURL2: TrustedURL = \"/\"", "story B.Default", "story A.Mine [v = 1]"} {
		if !strings.Contains(got, want) {
			t.Errorf("merged Document lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "story A.Mine [v = 2]") {
		t.Errorf("merge overwrote a hand-written story")
	}
	if !slices.Equal(report.Own, []string{"A.Mine"}) || !slices.Equal(report.Added, []string{"B.Default"}) {
		t.Errorf("report = %+v", report)
	}
	again, _, err := Merge(merged, desired, GeneratedMarker)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != got {
		t.Errorf("a second merge changed the Document:\n%s", again)
	}
}

// TestMerge_GIVENADocumentThatDoesNotParse_WHENMerged_THENRefuses leaves a
// broken Document alone.
func TestMerge_GIVENADocumentThatDoesNotParse_WHENMerged_THENRefuses(t *testing.T) {
	_, _, err := Merge([]byte("story A.B [v = \n"), Desired{Stories: []Block{{Key: "A.C", Text: "story A.C"}}}, GeneratedMarker)
	if err == nil {
		t.Fatal("Merge accepted a Document that does not parse")
	}
}
