package formatter

import (
	"testing"

	"github.com/karl-tye/ktml/internal/parser"
)

func TestFormatter_GIVENAStoryDocument_WHENFormatted_THENCanonicalAndIdempotent(t *testing.T) {
	source := "import { Card } from \"./card.ktml\"\nfixture   report :TrustedURL=\"/r\"\nstory Card.Running [title=\"Run\", stages=[[label=\"a\",note=None],[label=\"b\"]], progress=Running[callId=\"c\", link=report], tags=[], open=true]{\n\"Body\"\nslot footer { span \"F\" }\n}\nstory Card.Bare\n"
	want := "import { Card } from \"./card.ktml\"\n\nfixture report: TrustedURL = \"/r\"\n\nstory Card.Running [\n\ttitle = \"Run\",\n\tstages = [[label = \"a\", note = None], [label = \"b\"]],\n\tprogress = Running [callId = \"c\", link = report],\n\ttags = [],\n\topen = true,\n] {\n\t\"Body\"\n\tslot footer {\n\t\tspan {\n\t\t\t\"F\"\n\t\t}\n\t}\n}\n\nstory Card.Bare\n"
	p := parser.New([]byte(source))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := Format(doc)
	if got != want {
		t.Fatalf("Format() =\n%q\nwant\n%q", got, want)
	}
	again := parser.New([]byte(got))
	if Format(again.ParseDocument()) != got || again.Err() != nil {
		t.Fatalf("formatting is not idempotent: %v", again.Err())
	}
}
