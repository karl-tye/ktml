package rt

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestAttributes_GIVENBagOfEveryContext_WHENWritten_THENEscapesEachEntryForItsContext(t *testing.T) {
	bag := ktml.JoinAttributes(
		ktml.MustAttr("title", `"><script>x</script>`),
		ktml.MustBoolAttr("required", true),
		ktml.MustBoolAttr("hidden", false),
		ktml.MustURLAttr("href", ktml.NewURL("javascript:alert(1)")),
		ktml.MustURLAttr("action", ktml.NewURL("/a?b=1&c='2'")),
		ktml.MustTrustedURLAttr("data-src", ktml.TrustURL(`javascript:go("x")`)),
		ktml.MustJSONAttr("hx-vals", ktml.TrustJSON(`{"a":"</script>'"}`)),
		ktml.MustScriptAttr("@click", ktml.TrustScript(`go("a<b") && 'c'`)),
		ktml.MustScriptBoolAttr(":disabled", false),
		ktml.MustCSSAttr("style", ktml.TrustCSS(`width: 1%; content: "<x>"`)),
		ktml.MustAttr("class", "forwarded"),
		ktml.MustAttr("NAME", "forwarded"),
	)
	var builder strings.Builder
	w, owned := Wrap(&builder)
	if err := Attributes(w, bag, []string{"class", "name"}); err != nil {
		t.Fatal(err)
	}
	Finish(w, owned, nil)
	want := ` title="&#34;&gt;&lt;script&gt;x&lt;/script&gt;" required` +
		` href="about:invalid#ktml-unsafe-url" action="/a?b=1&amp;c=&#39;2&#39;"` +
		` data-src="javascript:go(&#34;x&#34;)" hx-vals="{&#34;a&#34;:&#34;&lt;/script&gt;&#39;&#34;}"` +
		` @click="go(&#34;a&lt;b&#34;) &amp;&amp; &#39;c&#39;" :disabled="false"` +
		` style="width: 1%; content: &#34;&lt;x&gt;&#34;"`
	if got := builder.String(); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestAttributes_GIVENEmptyBag_WHENWritten_THENDoesNotAllocate(t *testing.T) {
	var builder strings.Builder
	w, owned := Wrap(&builder)
	defer Finish(w, owned, nil)
	bag := ktml.JoinAttributes(ktml.MustAttr("title", "a"), ktml.MustBoolAttr("required", true))
	ownedNames := []string{"id"}
	if allocations := testing.AllocsPerRun(100, func() { _ = Attributes(w, bag, ownedNames) }); allocations != 0 {
		t.Fatalf("allocations = %v, want 0", allocations)
	}
}
