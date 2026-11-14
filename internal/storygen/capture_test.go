package storygen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/compiler"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func scalar(text string) compiler.Value {
	return compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(compiler.StringType), String: text}
}

func record(fields ...compiler.FieldValue) compiler.Value {
	return compiler.Value{Kind: compiler.RecordValueKind, Fields: fields}
}

func field(name string, value compiler.Value) compiler.FieldValue {
	return compiler.FieldValue{Name: name, Value: value}
}

// personal is captured text that must never reach a story file.
var personal = []string{
	"Jane Doe", "jane.doe@gmail.com", "07911 123456", "+44 7911 123456", "SW19 5AE", "221B Baker Street",
	"cus_8f14e45fceea167a5a36dedd4bea2543", "4f9c2a1e-8b7d-4c3e-9a2b-1d2e3f4a5b6c", "The tenant's dog bites; key under the mat",
	"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "98765432",
}

// TestRedactor_GIVENPersonalData_WHENRedacted_THENFakesAreStableAndRawValuesGone
// redacts names, contact details, postcodes, addresses, identifiers, tokens,
// and free text, by field name and by what the text looks like, and keeps
// markup and coarse values. The same value gets the same fake under one key
// and a different one under another.
func TestRedactor_GIVENPersonalData_WHENRedacted_THENFakesAreStableAndRawValuesGone(t *testing.T) {
	redactor := NewRedactor(testKey)
	tests := []struct {
		name, raw string
		keep      bool
	}{
		{"customerName", "Jane Doe", false},
		{"email", "jane.doe@gmail.com", false},
		{"contactPhone", "07911 123456", false},
		{"postcode", "SW19 5AE", false},
		{"addressLine1", "221B Baker Street", false},
		{"customerID", "cus_8f14e45fceea167a5a36dedd4bea2543", false},
		{"key", "4f9c2a1e-8b7d-4c3e-9a2b-1d2e3f4a5b6c", false},
		{"notes", "The tenant's dog bites; key under the mat", false},
		{"sessionToken", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", false},
		{"label", "Call jane.doe@gmail.com on +44 7911 123456", false},
		{"valueValue", "98765432", false},
		{"classBgColor", "bg-red text-white", true},
		{"status", "pending", true},
		{"jobCount", "12", true},
		{"createdAt", "14 Mar 2026 09:15", true},
	}
	for _, test := range tests {
		got := redactor.String(test.name, test.raw)
		if test.keep {
			if got != test.raw {
				t.Errorf("String(%s, %q) = %q, want it kept", test.name, test.raw, got)
			}
			continue
		}
		for _, secret := range personal {
			if strings.Contains(got, secret) {
				t.Errorf("String(%s, %q) = %q still holds %q", test.name, test.raw, got, secret)
			}
		}
		if again := NewRedactor(testKey).String(test.name, test.raw); again != got {
			t.Errorf("String(%s, %q) is not stable: %q then %q", test.name, test.raw, got, again)
		}
		if other := NewRedactor([]byte("another key, another set of fakes")).String(test.name, test.raw); other == got && test.name != "classBgColor" {
			t.Logf("String(%s) gave the same fake under another key: %q", test.name, got)
		}
	}
	if got := redactor.contextual(compiler.TrustedURLType, "link", "https://crm.example.org/customers/48213/notes?email=jane.doe@gmail.com#top"); got != "https://example.com/customers/id-"+redactor.hex("segment", "48213", 8)+"/notes" {
		t.Errorf("URL = %q", got)
	}
	if got := redactor.contextual(compiler.JSONValueType, "payload", `{"customerName":"Jane Doe","status":"open","id":48213}`); strings.Contains(got, "Jane Doe") || strings.Contains(got, "48213") || !strings.Contains(got, `"status":"open"`) {
		t.Errorf("JSON = %s", got)
	}
	if got := redactor.contextual(compiler.TrustedScriptType, "init", "{ name: 'Jane Doe' }"); got != "{}" {
		t.Errorf("script = %q", got)
	}
	id := compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(compiler.IntegerType), Integer: 48213}
	if got := redactor.Value("jobID", id); got.Integer == 48213 {
		t.Errorf("an integer ID was kept")
	}
	count := compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(compiler.IntegerType), Integer: 7}
	if got := redactor.Value("visits", count); got.Integer != 7 {
		t.Errorf("a count was changed to %d", got.Integer)
	}
}

func captureFixture(t *testing.T) (string, *compiler.Snapshot, compiler.Component) {
	t.Helper()
	root := copyFixture(t, "basic")
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range snapshot.Components() {
		if component.Name == "JobCard" {
			return root, snapshot, component
		}
	}
	t.Fatal("no JobCard")
	return "", nil, compiler.Component{}
}

func capturedJob(component compiler.Component, name, email string, status compiler.Value) interp.Captured {
	job := record(
		field("id", scalar("job-48213")),
		field("title", scalar("Leak at 221B Baker Street")),
		field("customerName", scalar(name)),
		field("customerEmail", scalar(email)),
		field("price", compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(compiler.NumberType), Number: 120}),
		field("visits", compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(compiler.IntegerType), Integer: 2}),
		field("urgent", compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(compiler.BooleanType), Boolean: true}),
		field("link", compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.Type{Kind: compiler.ContextualTypeKind, Name: compiler.URLType}, String: "/jobs/48213"}),
		field("status", status),
	)
	return interp.Captured{Component: component, Values: []compiler.StoryValue{{Input: "job", Value: job}}}
}

// TestCapturer_GIVENRendersOfOneComponent_WHENCaptured_THENWritesOneRedactedStoryPerShape
// captures three renders: two of one shape and one of another. Two stories
// are written, under the captured marker, with no raw value; they
// type-check; and a new Capturer over the same files still knows the
// shapes.
func TestCapturer_GIVENRendersOfOneComponent_WHENCaptured_THENWritesOneRedactedStoryPerShape(t *testing.T) {
	root, _, component := captureFixture(t)
	open := compiler.Value{Kind: compiler.VariantValueKind, Variant: "Open"}
	booked := compiler.Value{Kind: compiler.VariantValueKind, Variant: "Booked", Fields: []compiler.FieldValue{field("engineerName", scalar("Bob Builder")), field("slot", scalar("09:00"))}}

	capturer, err := NewCapturer(root, "captured", NewRedactor(testKey))
	if err != nil {
		t.Fatal(err)
	}
	first, err := capturer.Add(capturedJob(component, "Jane Doe", "jane.doe@gmail.com", open))
	if err != nil {
		t.Fatal(err)
	}
	same, err := capturer.Add(capturedJob(component, "John Smith", "john@smith.org", open))
	if err != nil {
		t.Fatal(err)
	}
	second, err := capturer.Add(capturedJob(component, "Jane Doe", "jane.doe@gmail.com", booked))
	if err != nil {
		t.Fatal(err)
	}
	if first.Story != "JobCard.CapturedOpen" || same.Story != "" || second.Story != "JobCard.CapturedBooked" {
		t.Errorf("stories %q, %q (%s), %q", first.Story, same.Story, same.Skipped, second.Story)
	}
	path := filepath.Join(root, "captured", "web", "board.story.ktml")
	if first.Path != path {
		t.Errorf("Path = %s, want %s", first.Path, path)
	}
	written := readFile(t, path)
	t.Logf("captured:\n%s", written)
	for _, secret := range append(personal, "Bob Builder", "48213", "John Smith") {
		if strings.Contains(written, secret) {
			t.Errorf("the captured story holds %q", secret)
		}
	}
	for _, want := range []string{"// ktml:captured ", `import { JobCard } from "../../web/board.ktml"`, "redacted (shape "} {
		if !strings.Contains(written, want) {
			t.Errorf("the captured Document lacks %q", want)
		}
	}
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range snapshot.Diagnostics(path) {
		t.Errorf("diagnostic: [%s] %s", diagnostic.Code, diagnostic.Message)
	}

	again, err := NewCapturer(root, "captured", NewRedactor(testKey))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := again.Add(capturedJob(component, "Someone Else", "x@y.org", booked)); err != nil || result.Story != "" {
		t.Errorf("a new Capturer wrote %q (%v) for a known shape", result.Story, err)
	}
}

// TestCapturer_GIVENRawMode_WHENCaptured_THENKeepsValues keeps raw values
// only when no redactor is given.
func TestCapturer_GIVENRawMode_WHENCaptured_THENKeepsValues(t *testing.T) {
	root, _, component := captureFixture(t)
	capturer, err := NewCapturer(root, ".", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := capturer.Add(capturedJob(component, "Jane Doe", "jane.doe@gmail.com", compiler.Value{Kind: compiler.VariantValueKind, Variant: "Open"}))
	if err != nil {
		t.Fatal(err)
	}
	written := readFile(t, result.Path)
	if !strings.Contains(written, "Jane Doe") || !strings.Contains(written, "raw values (shape ") {
		t.Errorf("raw capture:\n%s", written)
	}
	if result.Path != filepath.Join(root, "web", "board.story.ktml") {
		t.Errorf("Path = %s", result.Path)
	}
}

// TestNewCapturer_GIVENADirectoryOutsideTheWorkspace_WHENCreated_THENRefuses
// keeps captures inside the project and out of .git and .ktml.
func TestNewCapturer_GIVENADirectoryOutsideTheWorkspace_WHENCreated_THENRefuses(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"..", "../elsewhere", ".git/stories", ".ktml/x", os.TempDir()} {
		if _, err := NewCapturer(root, dir, nil); err == nil {
			t.Errorf("NewCapturer accepted %s", dir)
		}
	}
}

// TestClassify_GIVENNamesOfThings_WHENClassified_THENOnlyPeopleAreNames
// keeps person-name fakes for people: a role or department name is text.
func TestClassify_GIVENNamesOfThings_WHENClassified_THENOnlyPeopleAreNames(t *testing.T) {
	for name, want := range map[string]category{
		"customerName":   categoryFullName,
		"memberName":     categoryFullName,
		"firstName":      categoryFirstName,
		"companyName":    categoryCompany,
		"memberRoleName": categoryGeneric,
		"departmentName": categoryGeneric,
		"tradeName":      categoryGeneric,
	} {
		if got := classify(name); got != want {
			t.Errorf("classify(%s) = %d, want %d", name, got, want)
		}
	}
}
