package formatter

import "testing"

func TestFormat_GIVENConditionalAttributes_WHENFormatted_THENKeepsThemInAGroup(t *testing.T) {
	source := "Page [on: boolean, note?: string] {\n" +
		"option  (value=\"6\"   if on {selected}) \"6\"\n" +
		"a (\nhref=\"/x\"\n  if on { aria-current=\"page\"   class=\"on\" }   else if note? { title=\"{note}\" } else {}\n" +
		"  data-a=\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n) \"x\"\n}\n"
	want := "Page [on: boolean, note?: string] {\n" +
		"\toption (value=\"6\" if on { selected }) {\n\t\t\"6\"\n\t}\n" +
		"\ta (\n\t\thref=\"/x\"\n\t\tif on { aria-current=\"page\" class=\"on\" } else if note? { title=\"{note}\" } else {}\n" +
		"\t\tdata-a=\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n\t) {\n\t\t\"x\"\n\t}\n}\n"
	got := formatSource(t, source)
	if got != want {
		t.Fatalf("Format() =\n%s\nwant\n%s", got, want)
	}
	if again := formatSource(t, got); again != got {
		t.Fatalf("Format() is not idempotent:\n%s", again)
	}
}

func TestFormat_GIVENSpreadsAndBags_WHENFormatted_THENWritesThemCanonically(t *testing.T) {
	source := "Page [] {\ninput   type=\"text\"   ...attrs\nField (label=\"x\"\n attrs=(  required   if on {disabled} ...more ))\n}\n"
	want := "Page {\n\tinput type=\"text\" ...attrs\n\tField label=\"x\" attrs=(required if on { disabled } ...more)\n}\n"
	got := formatSource(t, source)
	if got != want {
		t.Fatalf("Format() =\n%s\nwant\n%s", got, want)
	}
	if again := formatSource(t, got); again != got {
		t.Fatalf("Format() is not idempotent:\n%s", again)
	}
}
