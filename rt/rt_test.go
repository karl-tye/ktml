package rt

import (
	"errors"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestPrimitives_GIVENValues_WHENWritten_THENEscapeAndFormatCanonically(t *testing.T) {
	tests := []struct {
		name  string
		write func(*Writer) error
		want  string
	}{
		{name: "static", write: func(w *Writer) error { return Raw(w, "<b>") }, want: "<b>"},
		{name: "text", write: func(w *Writer) error { return Text(w, `<a href="x">&'`) }, want: "&lt;a href=&#34;x&#34;&gt;&amp;&#39;"},
		{name: "text without escapes", write: func(w *Writer) error { return Text(w, "plain") }, want: "plain"},
		{name: "text with trailing run", write: func(w *Writer) error { return Text(w, "a<b") }, want: "a&lt;b"},
		{name: "attribute", write: func(w *Writer) error { return Attr(w, `"x"`) }, want: "&#34;x&#34;"},
		{name: "negative integer text", write: func(w *Writer) error { return TextInt(w, -9223372036854775808) }, want: "-9223372036854775808"},
		{name: "integer attribute", write: func(w *Writer) error { return AttrInt(w, 42) }, want: "42"},
		{name: "number", write: func(w *Writer) error { return TextNumber(w, 12.5) }, want: "12.5"},
		{name: "whole number", write: func(w *Writer) error { return AttrNumber(w, 3) }, want: "3"},
		{name: "large number", write: func(w *Writer) error { return TextNumber(w, 1e21) }, want: "1e+21"},
		{name: "small number", write: func(w *Writer) error { return TextNumber(w, 1e-7) }, want: "1e-07"},
		{name: "trusted html", write: func(w *Writer) error { return TrustedHTML(w, ktml.TrustHTML("<b>&</b>")) }, want: "<b>&</b>"},
		{name: "css", write: func(w *Writer) error { return CSS(w, ktml.TrustCSS(`width:"1"`)) }, want: "width:&#34;1&#34;"},
		{name: "json script", write: func(w *Writer) error {
			return JSONScript(w, ktml.TrustJSON("{\"a\":\"</script><!--&\u2028\u2029\"}"))
		}, want: `{"a":"\u003c/script\u003e\u003c!--\u0026\u2028\u2029"}`},
		{name: "script attribute", write: func(w *Writer) error { return ScriptAttr(w, ktml.TrustScript(`go("a<b", 'c') && d`)) }, want: "go(&#34;a&lt;b&#34;, &#39;c&#39;) &amp;&amp; d"},
		{name: "script boolean true", write: func(w *Writer) error { return ScriptBool(w, true) }, want: "true"},
		{name: "script boolean false", write: func(w *Writer) error { return ScriptBool(w, false) }, want: "false"},
		{name: "json attribute", write: func(w *Writer) error { return JSONAttr(w, ktml.TrustJSON(`{"a":"'&"}`)) }, want: "{&#34;a&#34;:&#34;&#39;&amp;&#34;}"},
		{name: "script", write: func(w *Writer) error { return Script(w, ktml.TrustScript("if (a < b) {}")) }, want: "if (a < b) {}"},
		{name: "script end tags", write: func(w *Writer) error {
			return Script(w, ktml.TrustScript(`x = '</script><!--</SCRIPT >'; y = /<\/scrip/; z = '<!-' + '</scri'; <`))
		}, want: `x = '<\/script><\!--<\/SCRIPT >'; y = /<\/scrip/; z = '<!-' + '</scri'; <`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var builder strings.Builder
			w, owned := Wrap(&builder)
			defer Finish(w, owned, nil)
			if err := tt.write(w); err != nil {
				t.Fatalf("write error = %v", err)
			}
			if got := builder.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestURL_GIVENSchemes_WHENWritten_THENAppliesPolicyAndEscapes(t *testing.T) {
	tests := []struct {
		name, raw, want string
		trusted         bool
	}{
		{name: "relative path", raw: "/public/quotes?token=a&b", want: "/public/quotes?token=a&amp;b"},
		{name: "https", raw: "https://example.com/r?x=\"1\"", want: "https://example.com/r?x=&#34;1&#34;"},
		{name: "uppercase scheme", raw: "HTTPS://example.com", want: "HTTPS://example.com"},
		{name: "mailto", raw: "mailto:a@b.c", want: "mailto:a@b.c"},
		{name: "colon after path", raw: "/a:b", want: "/a:b"},
		{name: "javascript", raw: "javascript:alert(1)", want: UnsafeURL},
		{name: "mixed case javascript", raw: "JaVaScRiPt:alert(1)", want: UnsafeURL},
		{name: "data", raw: "data:text/html,x", want: UnsafeURL},
		{name: "trusted bypasses policy", raw: "javascript:void(0)", want: "javascript:void(0)", trusted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var builder strings.Builder
			w, owned := Wrap(&builder)
			defer Finish(w, owned, nil)
			var err error
			if tt.trusted {
				err = TrustedURL(w, ktml.TrustURL(tt.raw))
			} else {
				err = URL(w, ktml.NewURL(tt.raw))
			}
			if err != nil || builder.String() != tt.want {
				t.Errorf("got %q, %v; want %q", builder.String(), err, tt.want)
			}
		})
	}
}

func TestInvalidState_GIVENComponentAndInput_WHENBuilt_THENIsTypedRenderError(t *testing.T) {
	err := InvalidState("example.com/app.Card", "payment")
	var renderError *ktml.RenderError
	if !errors.As(err, &renderError) || renderError.Kind != ktml.ErrInvalidState || renderError.Component != "example.com/app.Card" || renderError.Input != "payment" || renderError.Partial {
		t.Fatalf("InvalidState() = %#v", err)
	}
	if got := err.Error(); got != "ktml: invalid state in example.com/app.Card input payment" {
		t.Errorf("Error() = %q", got)
	}
}
