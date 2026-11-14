package ktml_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/rt"
)

func TestJSON_GIVENHostileStrings_WHENEncoded_THENTheyCannotEndTheirContainer(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "script end tag", value: "</script><b>x</b>", want: `"\u003c/script\u003e\u003cb\u003ex\u003c/b\u003e"`},
		{name: "upper-case end tag", value: "</SCRIPT >", want: `"\u003c/SCRIPT \u003e"`},
		{name: "comment opener", value: "<!--", want: `"\u003c!--"`},
		{name: "line and paragraph separators", value: "a\u2028b\u2029c", want: `"a\u2028b\u2029c"`},
		{name: "ampersand and quotes", value: `O'Neil & "Sons"`, want: `"O'Neil \u0026 \"Sons\""`},
		{name: "struct", value: struct {
			ID   string `json:"id"`
			Open bool   `json:"open"`
		}{ID: "</script>", Open: true}, want: `{"id":"\u003c/script\u003e","open":true}`},
		{name: "slice", value: []string{"a", "<!--"}, want: `["a","\u003c!--"]`},
		{name: "number", value: 12.5, want: `12.5`},
		{name: "nil", value: nil, want: `null`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := ktml.JSON(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if got := value.String(); got != test.want {
				t.Fatalf("JSON(%#v) = %s, want %s", test.value, got, test.want)
			}
			var decoded any
			if err := json.Unmarshal([]byte(value.String()), &decoded); err != nil {
				t.Fatalf("JSON(%#v) is not valid JSON: %v", test.value, err)
			}
		})
	}
}

func TestJSON_GIVENHostileStrings_WHENRenderedInAScriptOrAttribute_THENNoMarkupEscapes(t *testing.T) {
	value := ktml.MustJSON(map[string]string{"a": "</script><!--<b>\u2028&'\""})
	var body bytes.Buffer
	w, owned := rt.Wrap(&body)
	if err := rt.Finish(w, owned, rt.JSONScript(w, value)); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"</script", "<!--", "<b>", "\u2028"} {
		if strings.Contains(strings.ToLower(body.String()), forbidden) {
			t.Errorf("script body %s contains %q", body.String(), forbidden)
		}
	}
	var attribute bytes.Buffer
	w, owned = rt.Wrap(&attribute)
	if err := rt.Finish(w, owned, rt.JSONAttr(w, value)); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(attribute.String(), `"<>'`) {
		t.Errorf("attribute value %s contains a raw quote or angle bracket", attribute.String())
	}
}

func TestJSON_GIVENAValueEncodingJSONRejects_WHENEncoded_THENReturnsItsError(t *testing.T) {
	tests := []any{math.NaN(), math.Inf(1), make(chan int), func() {}, map[bool]string{true: "x"}}
	for _, value := range tests {
		got, err := ktml.JSON(value)
		var unsupportedType *json.UnsupportedTypeError
		var unsupportedValue *json.UnsupportedValueError
		if !errors.As(err, &unsupportedType) && !errors.As(err, &unsupportedValue) {
			t.Errorf("JSON(%T) error = %v, want encoding/json's error", value, err)
		}
		if got.String() != "" {
			t.Errorf("JSON(%T) = %q with an error, want the zero JSONValue", value, got.String())
		}
	}
}

func TestMustJSON_GIVENAValueEncodingJSONRejects_WHENEncoded_THENPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustJSON(NaN) did not panic")
		}
	}()
	ktml.MustJSON(math.NaN())
}

func TestMustJSON_GIVENAnEncodableValue_WHENEncoded_THENMatchesJSON(t *testing.T) {
	if got := ktml.MustJSON([]int{1, 2}).String(); got != "[1,2]" {
		t.Fatalf("MustJSON([1 2]) = %s, want [1,2]", got)
	}
}
