package formatter

import (
	"strings"
	"testing"
)

func TestFormat_GIVENFrameworkNamesDoctypeAndComments_WHENFormatted_THENPreservesThemCanonically(t *testing.T) {
	source := "Layout [] {\n" +
		"<!-- keep  {spacing} -->\n" +
		"  doctype   html\n" +
		"html   lang=\"en\" {\n" +
		"body   x-data   @keydown.slash.window=\"f()\"   :class=\"c\" hx-on::after-request=\"g()\" x-transition.opacity.duration.100ms {\n" +
		"<!--\n  multi\n    line\n-->\n" +
		"button @click.prevent \"go\"\n" +
		"}\n}\n}\n"
	want := "Layout {\n" +
		"\t<!-- keep  {spacing} -->\n" +
		"\tdoctype html\n" +
		"\thtml lang=\"en\" {\n" +
		"\t\tbody (\n\t\t\tx-data\n\t\t\t@keydown.slash.window=\"f()\"\n\t\t\t:class=\"c\"\n\t\t\thx-on::after-request=\"g()\"\n\t\t\tx-transition.opacity.duration.100ms\n\t\t) {\n" +
		"\t\t\t<!--\n  multi\n    line\n-->\n" +
		"\t\t\tbutton @click.prevent=\"go\"\n" +
		"\t\t}\n\t}\n}\n"
	got := formatSource(t, source)
	if got != want {
		t.Fatalf("Format() =\n%s\nwant\n%s", got, want)
	}
	if again := formatSource(t, got); again != got {
		t.Fatalf("Format() is not idempotent:\n%s", again)
	}
}

func TestFormat_GIVENLongHeaderWithFrameworkNames_WHENFormatted_THENUsesTheContinuationGroup(t *testing.T) {
	source := "Panel [] {\n\tform @submit.prevent=\"save()\" x-bind:class=\"{ busy: saving, 'border-red': failed }\" hx-on::after-request=\"reset()\" :disabled=\"busy\" {}\n}\n"
	got := formatSource(t, source)
	if !strings.Contains(got, "form (\n\t\t@submit.prevent=\"save()\"\n\t\tx-bind:class=") || !strings.Contains(got, "\n\t\t:disabled=\"busy\"\n\t)") {
		t.Fatalf("Format() =\n%s", got)
	}
	if again := formatSource(t, got); again != got {
		t.Fatalf("Format() is not idempotent:\n%s", again)
	}
}

func TestFormat_GIVENRawTextLiterals_WHENFormatted_THENPreservesTheirTextByteForByte(t *testing.T) {
	source := "Page [] {\nscript   ```\n  if (a) {\n\tgo(`${x}`) }\n```\n  style {\n```a{}```   ```b{}```\n}\n}\n"
	want := "Page {\n" +
		"\tscript {\n\t\t```\n  if (a) {\n\tgo(`${x}`) }\n```\n\t}\n" +
		"\tstyle {\n\t\t```a{}```\n\t\t```b{}```\n\t}\n}\n"
	got := formatSource(t, source)
	if got != want {
		t.Fatalf("Format() =\n%q\nwant\n%q", got, want)
	}
	if again := formatSource(t, got); again != got {
		t.Fatalf("Format() is not idempotent:\n%s", again)
	}
}
