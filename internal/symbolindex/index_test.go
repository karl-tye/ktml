package symbolindex

import "testing"

func span(line, start, end int) Range {
	return Range{Start: Position{line, start}, End: Position{line, end}}
}

func TestIndex_GIVENTwoProviders_WHENOneReferencesTheOthersSymbol_THENReferencesSpanBoth(t *testing.T) {
	index := New()
	index.Apply("gohtml", Result{Documents: []Document{{
		URI:     "file:///a.ktml",
		Symbols: []Symbol{{ID: "route:GET /x", Kind: Route, Name: "GET /x", Range: span(0, 0, 3)}},
	}}})
	index.Apply("htmx", Result{Documents: []Document{{
		URI:        "file:///b.ktml",
		References: []Reference{{Symbol: "route:GET /x", Kind: ReadReference, Range: span(4, 10, 14)}},
	}}})

	occurrence, ok := index.At("file:///b.ktml", Position{4, 12})
	if !ok || occurrence.Symbol != "route:GET /x" || occurrence.Declaration {
		t.Fatalf("At() = %+v, %v", occurrence, ok)
	}
	declarations := index.Declarations(occurrence.Symbol)
	if len(declarations) != 1 || declarations[0].URI != "file:///a.ktml" {
		t.Fatalf("Declarations() = %+v", declarations)
	}
	if uses := index.References(occurrence.Symbol); len(uses) != 1 || uses[0].URI != "file:///b.ktml" {
		t.Fatalf("References() = %+v", uses)
	}
}

func TestIndex_GIVENAContribution_WHENItsDocumentIsReplaced_THENOnlyTheNewSymbolsRemain(t *testing.T) {
	index := New()
	index.Apply("p", Result{Documents: []Document{{
		URI:        "file:///a.ktml",
		Symbols:    []Symbol{{ID: "Old", Kind: Component, Name: "Old", Range: span(0, 0, 3)}},
		References: []Reference{{Symbol: "Other", Kind: CallReference, Range: span(1, 1, 6), From: "Old"}},
	}}})
	index.Apply("p", Result{Documents: []Document{{
		URI:     "file:///a.ktml",
		Symbols: []Symbol{{ID: "New", Kind: Component, Name: "New", Range: span(0, 0, 3)}},
	}}})

	if declarations := index.Declarations("Old"); len(declarations) != 0 {
		t.Errorf("Declarations(Old) = %+v, want none", declarations)
	}
	if uses := index.References("Other"); len(uses) != 0 {
		t.Errorf("References(Other) = %+v, want none", uses)
	}
	if outgoing := index.Outgoing("Old"); len(outgoing) != 0 {
		t.Errorf("Outgoing(Old) = %+v, want none", outgoing)
	}
	if declarations := index.Declarations("New"); len(declarations) != 1 {
		t.Errorf("Declarations(New) = %+v", declarations)
	}
	if stats := index.Stats(); stats != (Stats{Documents: 1, Symbols: 1}) {
		t.Errorf("Stats() = %+v", stats)
	}
}

func TestIndex_GIVENRemovedURIs_WHENApplied_THENTheirContributionsAreDropped(t *testing.T) {
	index := New()
	index.Apply("p", Result{Documents: []Document{{URI: "file:///a.ktml", Symbols: []Symbol{{ID: "A", Range: span(0, 0, 1)}}}}})
	index.Apply("q", Result{Documents: []Document{{URI: "file:///a.ktml", References: []Reference{{Symbol: "A", Range: span(2, 0, 1)}}}}})
	index.Apply("p", Result{Removed: []string{"file:///a.ktml"}})

	if declarations := index.Declarations("A"); len(declarations) != 0 {
		t.Errorf("Declarations(A) = %+v, want none", declarations)
	}
	if uses := index.References("A"); len(uses) != 1 {
		t.Errorf("References(A) = %+v, want the other provider's use", uses)
	}
}

func TestIndex_GIVENNestedRanges_WHENLookedUp_THENTheInnermostWins(t *testing.T) {
	index := New()
	index.Apply("p", Result{Documents: []Document{{
		URI:        "file:///a.ktml",
		Symbols:    []Symbol{{ID: "Outer", Range: span(0, 0, 20)}},
		References: []Reference{{Symbol: "Inner", Range: span(0, 5, 9)}},
	}}})
	if occurrence, _ := index.At("file:///a.ktml", Position{0, 6}); occurrence.Symbol != "Inner" {
		t.Errorf("At(0:6) = %+v, want Inner", occurrence)
	}
	if occurrence, _ := index.At("file:///a.ktml", Position{0, 15}); occurrence.Symbol != "Outer" {
		t.Errorf("At(0:15) = %+v, want Outer", occurrence)
	}
	if _, ok := index.At("file:///a.ktml", Position{1, 0}); ok {
		t.Error("At(1:0) found a symbol off every range")
	}
}

func TestIndex_GIVENCallEdges_WHENQueried_THENOutgoingListsTheCallees(t *testing.T) {
	index := New()
	index.Apply("p", Result{Documents: []Document{{
		URI:     "file:///a.ktml",
		Symbols: []Symbol{{ID: "Page", Kind: Component, Range: span(0, 0, 4)}, {ID: "Card", Kind: Component, Range: span(5, 0, 4)}},
		References: []Reference{
			{Symbol: "Card", Kind: CallReference, Range: span(1, 1, 5), From: "Page"},
			{Symbol: "Card", Kind: CallReference, Range: span(2, 1, 5), From: "Page"},
		},
	}}})
	outgoing := index.Outgoing("Page")
	if len(outgoing) != 2 || outgoing[0].Symbol != "Card" || outgoing[0].Range.Start.Line != 1 {
		t.Fatalf("Outgoing(Page) = %+v", outgoing)
	}
	if uses := index.References("Card"); len(uses) != 2 || uses[0].From != "Page" {
		t.Fatalf("References(Card) = %+v", uses)
	}
}
