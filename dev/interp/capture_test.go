package interp_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/compiler"
)

// These types have the shapes the generator gives the capture fixture's
// components, so the program reads them as it reads generated props.

type JobCard struct {
	Job      captureJob
	Note     *string
	Link     ktml.TrustedURL
	Status   captureStatus
	Attrs    ktml.Attributes
	Children ktml.Component
}

func (JobCard) Render(context.Context, io.Writer) error { return nil }

type captureJob struct {
	Id     string
	Visits int64
	Rows   []captureRow
}

type captureRow struct {
	Key   string
	Label string
}

type captureVariant string

func (v captureVariant) String() string { return string(v) }

type captureStatus interface{ Variant() captureVariant }

type captureOpen struct{}

func (captureOpen) Variant() captureVariant { return "Open" }

type captureBooked struct{ payload captureBookedPayload }

type captureBookedPayload struct{ EngineerName string }

func (captureBooked) Variant() captureVariant { return "Booked" }

func captureProgram(t *testing.T) *interp.Program {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"model.ktmlschema": "record Job [\n\tid: string,\n\tvisits: integer,\n\trows: [Row],\n]\n\nrecord Row [\n\tkey: string,\n\tlabel: string,\n]\n\nstate Status {\n\tOpen,\n\tBooked [\n\t\tengineerName: string,\n\t],\n}\n",
		"card.ktml":        "import { Job, Row, Status } from \"./model.ktmlschema\"\n\nJobCard [job: Job, note?: string, link: TrustedURL, status: Status, attrs: Attributes, children?: content] {\n\tdiv ...attrs {\n\t\ta href=\"{link}\" \"{job.id}\"\n\t\tchildren\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Valid() {
		t.Fatalf("fixture: %v", snapshot.Err())
	}
	packagePath := reflect.TypeOf(JobCard{}).PkgPath()
	program, err := interp.New(snapshot, interp.Options{Packages: func(string) (string, error) { return packagePath, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// TestCapture_GIVENGeneratedShapedProps_WHENCaptured_THENReadsTypedStoryValues
// reads props back into story values: records by field, collections by
// item, a state's Variant and payload, a trusted value's text, an absent
// optional left out, and the content inputs filled.
func TestCapture_GIVENGeneratedShapedProps_WHENCaptured_THENReadsTypedStoryValues(t *testing.T) {
	program := captureProgram(t)
	props := JobCard{
		Job:      captureJob{Id: "job-42", Visits: 3, Rows: []captureRow{{Key: "a", Label: "First"}, {Key: "b", Label: "Second"}}},
		Link:     ktml.TrustURL("/jobs/42"),
		Status:   captureBooked{payload: captureBookedPayload{EngineerName: "Sam Engineer"}},
		Attrs:    mustAttr(t, "data-owner", "sam@example.com"),
		Children: ktml.ComponentFunc(func(context.Context, io.Writer) error { return nil }),
	}
	captured, err := program.Capture(props)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Component.Name != "JobCard" || captured.Unsupported != "" {
		t.Fatalf("captured %s, unsupported %q", captured.Component.Name, captured.Unsupported)
	}
	if len(captured.Content) != 1 || captured.Content[0] != "children" {
		t.Errorf("Content = %v, want children", captured.Content)
	}
	inputs := map[string]compiler.Value{}
	for _, value := range captured.Values {
		inputs[value.Input] = value.Value
	}
	if _, ok := inputs["note"]; ok {
		t.Errorf("the absent optional note was captured")
	}
	job := inputs["job"]
	if id, _ := job.Field("id"); id.String != "job-42" {
		t.Errorf("job.id = %q", id.String)
	}
	if visits, _ := job.Field("visits"); visits.Integer != 3 {
		t.Errorf("job.visits = %d", visits.Integer)
	}
	if rows, _ := job.Field("rows"); len(rows.Items) != 2 {
		t.Errorf("job.rows has %d items", len(rows.Items))
	} else if label, _ := rows.Items[1].Field("label"); label.String != "Second" {
		t.Errorf("job.rows[1].label = %q", label.String)
	}
	if link := inputs["link"]; link.Kind != compiler.FixtureValueKind || link.String != "/jobs/42" {
		t.Errorf("link = %+v", link)
	}
	if attrs := inputs["attrs"]; attrs.Kind != compiler.AttributesValueKind {
		t.Errorf("attrs = %+v, want the empty bag (a story gives no other)", attrs)
	}
	status := inputs["status"]
	if engineer, _ := status.Field("engineerName"); status.Variant != "Booked" || engineer.String != "Sam Engineer" {
		t.Errorf("status = %+v", status)
	}

	open, err := program.Capture(JobCard{Status: captureOpen{}, Link: ktml.TrustURL("/")})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range open.Values {
		if value.Input == "status" && (value.Value.Variant != "Open" || value.Value.Fields != nil) {
			t.Errorf("status = %+v, want Open without a payload", value.Value)
		}
	}
	if open.Unsupported != "" {
		t.Errorf("Unsupported = %q", open.Unsupported)
	}

	missing, err := program.Capture(JobCard{Link: ktml.TrustURL("/")})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Unsupported != "status" {
		t.Errorf("a nil required state: Unsupported = %q, want status", missing.Unsupported)
	}
}

func mustAttr(t *testing.T, name, value string) ktml.Attributes {
	t.Helper()
	attrs, err := ktml.Attr(name, value)
	if err != nil {
		t.Fatal(err)
	}
	return attrs
}
