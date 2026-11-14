package ktml_test

import (
	"errors"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestAttributes_GIVENConstructors_WHENNameAndContextDisagree_THENReturnAttributeErrors(t *testing.T) {
	tests := []struct {
		name  string
		build func() (ktml.Attributes, error)
		ok    bool
	}{
		{"plain text", func() (ktml.Attributes, error) { return ktml.Attr("hx-target", "#x") }, true},
		{"text on href", func() (ktml.Attributes, error) { return ktml.Attr("href", "javascript:alert(1)") }, false},
		{"text on upper-case SRC", func() (ktml.Attributes, error) { return ktml.Attr("SRC", "javascript:x") }, false},
		{"text on hx-post", func() (ktml.Attributes, error) { return ktml.Attr("hx-post", "/x") }, false},
		{"text on data-hx-get", func() (ktml.Attributes, error) { return ktml.Attr("data-hx-get", "/x") }, false},
		{"json on href", func() (ktml.Attributes, error) { return ktml.JSONAttr("href", ktml.TrustJSON("{}")) }, false},
		{"url on href", func() (ktml.Attributes, error) { return ktml.URLAttr("href", ktml.NewURL("/x")) }, true},
		{"url on data-href", func() (ktml.Attributes, error) { return ktml.URLAttr("data-href", ktml.NewURL("/x")) }, true},
		{"trusted url on action", func() (ktml.Attributes, error) {
			return ktml.TrustedURLAttr("action", ktml.TrustURL("/x"))
		}, true},
		{"presence on href", func() (ktml.Attributes, error) { return ktml.BoolAttr("href", true) }, true},
		{"text on onclick", func() (ktml.Attributes, error) { return ktml.Attr("onclick", "alert(1)") }, false},
		{"text on upper-case ONCLICK", func() (ktml.Attributes, error) { return ktml.Attr("ONCLICK", "alert(1)") }, false},
		{"text on @click", func() (ktml.Attributes, error) { return ktml.Attr("@click", "x") }, false},
		{"text on x-data", func() (ktml.Attributes, error) { return ktml.Attr("x-data", "{}") }, false},
		{"text on hx-on::after-request", func() (ktml.Attributes, error) { return ktml.Attr("hx-on::after-request", "x") }, false},
		{"text on data-hx-vals", func() (ktml.Attributes, error) { return ktml.Attr("data-hx-vals", "js:x") }, false},
		{"text on style", func() (ktml.Attributes, error) { return ktml.Attr("Style", "x") }, false},
		{"text on srcdoc", func() (ktml.Attributes, error) { return ktml.Attr("srcdoc", "<script>alert(1)</script>") }, false},
		{"text on upper-case SRCDOC", func() (ktml.Attributes, error) { return ktml.Attr("SRCDOC", "<b>x</b>") }, false},
		{"json on srcdoc", func() (ktml.Attributes, error) { return ktml.JSONAttr("srcdoc", ktml.TrustJSON(`"x"`)) }, false},
		{"url on srcdoc", func() (ktml.Attributes, error) { return ktml.URLAttr("srcdoc", ktml.NewURL("/x")) }, false},
		{"presence on srcdoc", func() (ktml.Attributes, error) { return ktml.BoolAttr("srcdoc", true) }, true},
		{"url on onclick", func() (ktml.Attributes, error) { return ktml.URLAttr("onclick", ktml.NewURL("/x")) }, false},
		{"trusted url on :href", func() (ktml.Attributes, error) {
			return ktml.TrustedURLAttr(":href", ktml.TrustURL("javascript:x"))
		}, false},
		{"script on plain", func() (ktml.Attributes, error) { return ktml.ScriptAttr("title", ktml.TrustScript("x")) }, false},
		{"script on @click", func() (ktml.Attributes, error) { return ktml.ScriptAttr("@click", ktml.TrustScript("x")) }, true},
		{"script bool on :disabled", func() (ktml.Attributes, error) { return ktml.ScriptBoolAttr(":disabled", true) }, true},
		{"script bool on plain", func() (ktml.Attributes, error) { return ktml.ScriptBoolAttr("disabled", true) }, false},
		{"json on x-data", func() (ktml.Attributes, error) { return ktml.JSONAttr("x-data", ktml.TrustJSON("{}")) }, true},
		{"json on style", func() (ktml.Attributes, error) { return ktml.JSONAttr("style", ktml.TrustJSON("{}")) }, false},
		{"css on style", func() (ktml.Attributes, error) { return ktml.CSSAttr("style", ktml.TrustCSS("a:b")) }, true},
		{"css on title", func() (ktml.Attributes, error) { return ktml.CSSAttr("title", ktml.TrustCSS("a:b")) }, false},
		{"presence on x-cloak", func() (ktml.Attributes, error) { return ktml.BoolAttr("x-cloak", true) }, true},
		{"invalid name with quote", func() (ktml.Attributes, error) { return ktml.Attr(`a"b`, "x") }, false},
		{"invalid name with space", func() (ktml.Attributes, error) { return ktml.Attr("a b", "x") }, false},
		{"invalid name with angle", func() (ktml.Attributes, error) { return ktml.BoolAttr("a><script", true) }, false},
		{"empty name", func() (ktml.Attributes, error) { return ktml.Attr("", "x") }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bag, err := test.build()
			var attributeError *ktml.AttributeError
			switch {
			case test.ok && err != nil:
				t.Fatalf("error = %v", err)
			case test.ok && bag.Len() != 1:
				t.Fatalf("Len() = %d", bag.Len())
			case !test.ok && !errors.As(err, &attributeError):
				t.Fatalf("error = %v, want *AttributeError", err)
			case !test.ok && bag.Len() != 0:
				t.Fatalf("Len() = %d after an error", bag.Len())
			}
		})
	}
}

func TestAttributes_GIVENMustOnAnInvalidContext_WHENCalled_THENPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustAttr did not panic")
		}
	}()
	ktml.MustAttr("onclick", "x")
}

func TestJoinAttributes_GIVENRepeatedNames_WHENJoined_THENKeepsTheFirstInOrder(t *testing.T) {
	bag := ktml.JoinAttributes(
		ktml.MustAttr("title", "a"),
		ktml.JoinAttributes(ktml.MustBoolAttr("required", true), ktml.MustAttr("TITLE", "b")),
		ktml.Attributes{},
		ktml.MustAttr("name", "n"),
	)
	var got []string
	for index := range bag.Len() {
		got = append(got, bag.At(index).Name()+"="+bag.At(index).Value())
	}
	if want := []string{"title=a", "required=", "name=n"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}
