package formatter

import "testing"

func TestFormat_GIVENKeyedRepetitions_WHENFormatted_THENUsesCanonicalLayoutAndKeepsKeys(t *testing.T) {
	source := `Board [columns:[Column],tags:[string]] {
ul{for   column in columns   key column.id {li "{column.title}"
for card in column.cards key card.id{p "{card.title}"}}}
for tag in tags key tag{}
li class="x"
for tag in tags key tag { "{tag}" }
}`
	want := `Board [columns: [Column], tags: [string]] {
	ul {
		for column in columns key column.id {
			li {
				"{column.title}"
			}
			for card in column.cards key card.id {
				p {
					"{card.title}"
				}
			}
		}
	}
	for tag in tags key tag {}
	li class="x"
	for tag in tags key tag {
		"{tag}"
	}
}
`
	got := formatSource(t, source)
	if got != want {
		t.Fatalf("Format() =\n%s\nwant\n%s", got, want)
	}
	if again := formatSource(t, got); again != got {
		t.Fatalf("Format() is not idempotent:\n%s", again)
	}
	if original, formatted := normalizedShape(t, source), normalizedShape(t, got); original != formatted {
		t.Fatalf("parse/format/parse changed the shape:\n%s\n---\n%s", original, formatted)
	}
}
