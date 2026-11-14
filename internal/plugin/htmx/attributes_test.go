package htmx

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func indexOf(routes ...string) RouteIndex {
	var index RouteIndex
	for _, entry := range routes {
		method, path, _ := strings.Cut(entry, " ")
		route := Route{Method: method, Path: path, Params: routeParams(path)}
		if strings.HasSuffix(path, "+slash") {
			route.Path = strings.TrimSuffix(path, "+slash")
			route.SlashOptional = true
		}
		index.Routes = append(index.Routes, route)
	}
	return index
}

func TestDiagnoseAttribute_GIVENAttributePaths_WHENCheckedAgainstRoutes_THENReportsPreciseProblems(t *testing.T) {
	index := indexOf(
		"POST /vendors/{id}/approve",
		"GET /vendors/{id}",
		"GET /vendors+slash",
		"GET /threads/{id}/attachments/{attachmentID}/file",
		"* /static/*",
		"GET /search",
	)
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{"interpolated parameter", `hx-post="/vendors/{vendor.id}/approve"`, nil},
		{"concrete parameter value", `hx-post="/vendors/42/approve"`, nil},
		{"chi-style parameter", `hx-post="/vendors/{id}/approve"`, nil},
		{"query and fragment", `hx-get="/search?q={query}&page=2#top"`, nil},
		{"optional trailing slash", `hx-get="/vendors/"`, nil},
		{"wildcard subtree", `hx-get="/static/js/{file}"`, nil},
		{"fully dynamic value", `hx-get="{panel.next.get}"`, nil},
		{"relative value", `hx-get="details"`, nil},
		{"absolute URL", `hx-get="https://example.com/x"`, nil},
		{"method mismatch", `hx-delete="/vendors/{vendor.id}/approve"`,
			[]string{"htmx-method-mismatch@11-39: No DELETE Chi route matches /vendors/{vendor.id}/approve; it is registered for POST"}},
		{"missing parameter", `hx-post="/vendors/approve"`,
			[]string{"htmx-missing-path-param@9-25: /vendors/approve is missing path parameter {id} of POST /vendors/{id}/approve"}},
		{"extra parameter", `hx-post="/vendors/{vendor.id}/{draft.id}/approve"`,
			[]string{"htmx-extra-path-param@30-40: {draft.id} is not a path parameter of POST /vendors/{id}/approve"}},
		{"interpolation at a static segment", `hx-post="/vendors/{vendor.id}/{action}"`,
			[]string{`htmx-path-param-mismatch@30-38: {action} is interpolated where POST /vendors/{id}/approve has the static segment "approve"`}},
		{"missing second parameter", `hx-get="/threads/{thread.id}/attachments/file"`,
			[]string{"htmx-missing-path-param@8-45: /threads/{thread.id}/attachments/file is missing path parameter {attachmentID} of GET /threads/{id}/attachments/{attachmentID}/file"}},
		{"typo in a static segment", `hx-post="/vendors/{vendor.id}/aprove"`,
			[]string{"htmx-missing-route@9-36: No Chi route matches /vendors/{vendor.id}/aprove; did you mean /vendors/{id}/approve?"}},
		{"interpolation where a longer route is static", `hx-get="/threads/{thread.id}/{x}"`,
			[]string{"htmx-missing-route@8-32: No Chi route matches /threads/{thread.id}/{x}"}},
		{"unknown route", `hx-get="/nowhere"`,
			[]string{"htmx-missing-route@8-16: No Chi route matches /nowhere"}},
		{"backtick value is literal", "hx-post=`/vendors/{id}/approve`", nil},
		{"literal braces are not interpolation", "hx-get=`/vendors/{ x }`", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attributes := scanAttributes(test.source)
			if len(attributes) != 1 {
				t.Fatalf("attributes = %#v, want one", attributes)
			}
			var got []string
			for _, problem := range diagnoseAttribute(attributes[0], index) {
				got = append(got, problem.code+"@"+strconv.Itoa(problem.start)+"-"+strconv.Itoa(problem.end)+": "+problem.message)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("problems =\n%q\nwant\n%q", got, test.want)
			}
		})
	}
}

func TestRewriteAttribute_GIVENRenamedRoute_WHENRewritingConsumers_THENKeepsValuesAndQuery(t *testing.T) {
	url, ok := parseAttributeURL("/vendors/{vendor.id}/approve?next=/x", false)
	if !ok {
		t.Fatal("attribute is not analysable")
	}
	got, ok := rewriteAttribute(url, "/vendors/{id}/approve", "/admin/vendors/{id}/approval")
	if !ok || got != "/admin/vendors/{vendor.id}/approval?next=/x" {
		t.Errorf("rewrite = %q, %t", got, ok)
	}
}

func TestRouteSnippet_GIVENParameters_WHENCompleting_THENEachBecomesATabStop(t *testing.T) {
	if got, want := routeSnippet("/vendors/{id}/invoices/{invoiceID:[0-9]+}"), `/vendors/${1:{id\}}/invoices/${2:{invoiceID:[0-9]+\}}`; got != want {
		t.Errorf("snippet = %q, want %q", got, want)
	}
}
