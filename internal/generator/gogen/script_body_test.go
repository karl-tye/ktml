package gogen_test

import "testing"

func TestGenerate_GIVENScriptBodiesWithDataPaths_WHENRendered_THENEmbedsEachValueSafely(t *testing.T) {
	generated := generateWorkspaceSource(t, `Chart [id: JSONValue, data: JSONValue, boot: TrustedScript, debug: boolean] {
	script type="application/json" id="chart-data" "{data}"
	script "createChart({id}, {data}); if (a < b && c) go();"
	script "window.debug = {debug}; {boot}"
}`)
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestScriptBody(t *testing.T) {
	chart := Chart{
		Id: ktml.TrustJSON("\"c\""),
		Data: ktml.TrustJSON("{\"k\":\"</script><!-- & \u2028\"}"),
		Boot: ktml.TrustScript("x = '</SCRIPT><script>alert(1)</script><!--';"),
		Debug: true,
	}
	var output strings.Builder
	if err := chart.Render(context.Background(), &output); err != nil { t.Fatal(err) }
	want := "<script type=\"application/json\" id=\"chart-data\">{\"k\":\"\\u003c/script\\u003e\\u003c!-- \\u0026 \\u2028\"}</script>" +
		"<script>createChart(\"c\", {\"k\":\"\\u003c/script\\u003e\\u003c!-- \\u0026 \\u2028\"}); if (a < b && c) go();</script>" +
		"<script>window.debug = true; x = '<\\/SCRIPT><script>alert(1)<\\/script><\\!--';</script>"
	if got := output.String(); got != want {
		t.Fatalf("rendered\n%s\nwant\n%s", got, want)
	}
	if strings.Count(output.String(), "</script>") != 3 {
		t.Fatalf("a value ended a script element: %s", output.String())
	}
}
`)
}
