package formatter

import "testing"

func TestFormat_GIVENStatesAndClauseGroups_WHENFormatted_THENUsesCanonicalLayout(t *testing.T) {
	source := `state Search {Loading,Loaded [items:[Item],total?:integer],Failed[message:string],}
state Empty {}
Results [state:Search,query:string]
Results [state=Loading] { "wait" }
Results [ state = Search.Loaded , items , total as count ] { div }
Results [state=Failed,message as text] {}
Note [note?:string] default { span }
Note [note=Some,value as text] { "{text}" }
Toggle []
Toggle [on=true] {}`
	want := `state Search {
	Loading,
	Loaded [
		items: [Item],
		total?: integer,
	],
	Failed [
		message: string,
	],
}

state Empty {}

Results [state: Search, query: string]

Results [state = Loading] {
	"wait"
}

Results [state = Search.Loaded, items, total as count] {
	div
}

Results [state = Failed, message as text] {

}

Note [note?: string] default {
	span
}

Note [note = Some, value as text] {
	"{text}"
}

Toggle []

Toggle [on = true] {

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
