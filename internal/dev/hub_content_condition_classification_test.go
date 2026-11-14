package dev

import "testing"

const contentConditionCard = `Box [extra?: content] {
	div {
		extra
	}
}

Card [on: boolean, note?: string, actions?: content] {
	if on {
		p "on"
	}
	Box {
		slot extra {
			span "x"
		}
	}
	actions
}

Wrap [] {
	div {
		children
	}
}
`

func TestGetChangedComponent_GIVENContentAndConditionEdits_WHENClassified_THENPatchesOnlyCompatibleChanges(t *testing.T) {
	card := func(signature, body string) string {
		return `Box [extra?: content] {
	div {
		extra
	}
}

Card ` + signature + ` {
` + body + `
}

Wrap [] {
	div {
		children
	}
}
`
	}
	const signature = "[on: boolean, note?: string, actions?: content]"
	const body = "\tif on {\n\t\tp \"on\"\n\t}\n\tBox {\n\t\tslot extra {\n\t\t\tspan \"x\"\n\t\t}\n\t}\n\tactions"
	tests := []struct {
		name, updated, want string
	}{
		{name: "unchanged", updated: contentConditionCard, want: ""},
		{name: "condition body edit", updated: card(signature, "\tif on {\n\t\tp \"ON\"\n\t}\n\tBox {\n\t\tslot extra {\n\t\t\tspan \"x\"\n\t\t}\n\t}\n\tactions"), want: "Card"},
		{name: "condition expression edit", updated: card(signature, "\tif !on || note? {\n\t\tp \"on\"\n\t}\n\tBox {\n\t\tslot extra {\n\t\t\tspan \"x\"\n\t\t}\n\t}\n\tactions"), want: "Card"},
		{name: "else branch added", updated: card(signature, "\tif on {\n\t\tp \"on\"\n\t} else {\n\t\tp \"off\"\n\t}\n\tBox {\n\t\tslot extra {\n\t\t\tspan \"x\"\n\t\t}\n\t}\n\tactions"), want: "Card"},
		{name: "slot fill body edit", updated: card(signature, "\tif on {\n\t\tp \"on\"\n\t}\n\tBox {\n\t\tslot extra {\n\t\t\tspan \"y\"\n\t\t}\n\t}\n\tactions"), want: "Card"},
		{name: "optional content added", updated: card("[on: boolean, note?: string, actions?: content, help?: content]", body), want: "Card"},
		{name: "required content added", updated: card("[on: boolean, note?: string, actions?: content, help: content]", body), want: ""},
		{name: "content made required", updated: card("[on: boolean, note?: string, actions: content]", body), want: ""},
		{name: "implicit children outlet added", updated: card("[on: boolean, note?: string, actions?: content]", body+"\n\tchildren"), want: "Card"},
		{name: "implicit children outlet removed", updated: `Box [extra?: content] {
	div {
		extra
	}
}

Card [on: boolean, note?: string, actions?: content] {
` + body + `
}

Wrap [] {
	div
}
`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getChangedComponent(compileHMRDocument(t, contentConditionCard), compileHMRDocument(t, tt.updated))
			if got != tt.want {
				t.Errorf("getChangedComponent() = %q, want %q", got, tt.want)
			}
		})
	}
}
