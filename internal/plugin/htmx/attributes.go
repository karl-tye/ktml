package htmx

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// Route attributes match only as whole attribute names (optionally
	// data-prefixed), so x-bind:hx-get and :hx-get, which Alpine.js
	// evaluates, are not routes. Backtick values are literal in KTML.
	completeAttributePattern = regexp.MustCompile("(?:^|[\\s({])(?:data-)?hx-(get|post|put|patch|delete)\\s*=\\s*(?:\"([^\"]*)|`([^`]*))$")
	attributePattern         = regexp.MustCompile("(?:^|[\\s({])(?:data-)?hx-(get|post|put|patch|delete)\\s*=\\s*(?:\"([^\"]*)\"|`([^`]*)`)")
)

// htmxAttribute is one hx-<method> attribute in a KTML document. Offsets are
// byte offsets of the value between its quotes.
type htmxAttribute struct {
	Method     string
	Start, End int
	ValueStart int
	ValueEnd   int
	Value      string
	Literal    bool
}

func scanAttributes(source string) []htmxAttribute {
	var attributes []htmxAttribute
	for _, match := range attributePattern.FindAllStringSubmatchIndex(source, -1) {
		attribute := htmxAttribute{Method: strings.ToUpper(source[match[2]:match[3]]), Start: match[0], End: match[1]}
		if match[4] >= 0 {
			attribute.ValueStart, attribute.ValueEnd = match[4], match[5]
		} else {
			attribute.ValueStart, attribute.ValueEnd = match[6], match[7]
			attribute.Literal = true
		}
		attribute.Value = source[attribute.ValueStart:attribute.ValueEnd]
		attributes = append(attributes, attribute)
	}
	return attributes
}

func attributeAt(source string, offset int) (htmxAttribute, bool) {
	for _, attribute := range scanAttributes(source) {
		if offset >= attribute.Start && offset <= attribute.End {
			return attribute, true
		}
	}
	return htmxAttribute{}, false
}

// urlSegment is one /-separated part of a path. In an attribute a segment
// is dynamic when it holds a KTML interpolation; in a route it is dynamic
// when it holds a Chi parameter.
type urlSegment struct {
	Text       string
	Dynamic    bool
	Start, End int
}

// attributeURL is the analysable path of an attribute value: a rooted path
// with the query and fragment split off.
type attributeURL struct {
	Path     string
	Suffix   string
	Segments []urlSegment
}

// parseAttributeURL reports false for values that cannot be checked: empty,
// relative, absolute, or starting with an interpolation.
func parseAttributeURL(value string, literal bool) (attributeURL, bool) {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return attributeURL{}, false
	}
	end := len(value)
	for i := 0; i < len(value); i++ {
		if !literal && isInterpolationStart(value, i) {
			i = interpolationEnd(value, i) - 1
			continue
		}
		if value[i] == '?' || value[i] == '#' {
			end = i
			break
		}
	}
	url := attributeURL{Path: value[:end], Suffix: value[end:]}
	url.Segments = splitSegments(url.Path, func(text string) bool {
		return !literal && containsInterpolation(text)
	}, func(i int) int {
		if !literal && isInterpolationStart(url.Path, i) {
			return interpolationEnd(url.Path, i)
		}
		return i + 1
	})
	return url, true
}

func routeSegments(path string) []urlSegment {
	return splitSegments(path, func(text string) bool {
		return strings.Contains(text, "{") || text == "*"
	}, func(i int) int {
		if path[i] != '{' {
			return i + 1
		}
		depth := 0
		for j := i; j < len(path); j++ {
			switch path[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return j + 1
				}
			}
		}
		return len(path)
	})
}

// splitSegments splits a rooted path on slashes outside braces; the leading
// slash produces no segment.
func splitSegments(path string, dynamic func(string) bool, advance func(int) int) []urlSegment {
	var segments []urlSegment
	start := 1
	for i := 1; i <= len(path); {
		if i == len(path) || path[i] == '/' {
			text := path[start:i]
			segments = append(segments, urlSegment{Text: text, Dynamic: dynamic(text), Start: start, End: i})
			start = i + 1
			i++
			continue
		}
		i = advance(i)
	}
	return segments
}

// isInterpolationStart follows KTML: { immediately followed by an
// identifier starts an interpolation.
func isInterpolationStart(value string, i int) bool {
	if value[i] != '{' || i+1 >= len(value) {
		return false
	}
	next := value[i+1]
	return next == '_' || next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z'
}

func interpolationEnd(value string, i int) int {
	if end := strings.IndexByte(value[i:], '}'); end >= 0 {
		return i + end + 1
	}
	return len(value)
}

func containsInterpolation(text string) bool {
	for i := range len(text) {
		if isInterpolationStart(text, i) {
			return true
		}
	}
	return false
}

// routeMatch is the result of comparing an attribute path with a route.
type routeMatch struct {
	ok bool
	// staticAt is the attribute segment interpolated where the route has a
	// static segment, or -1.
	staticAt int
}

func matchRoute(url attributeURL, route Route) routeMatch {
	routeSegs := routeSegments(route.Path)
	attrSegs := url.Segments
	if route.SlashOptional && len(attrSegs) == len(routeSegs)+1 && attrSegs[len(attrSegs)-1].Text == "" {
		attrSegs = attrSegs[:len(attrSegs)-1]
	}
	staticAt := -1
	for i, segment := range routeSegs {
		if segment.Text == "*" && i == len(routeSegs)-1 {
			return routeMatch{ok: len(attrSegs) >= i && staticAt < 0, staticAt: staticAt}
		}
		if i >= len(attrSegs) {
			return routeMatch{staticAt: -1}
		}
		attr := attrSegs[i]
		switch {
		case segment.Dynamic:
			if attr.Text == "" {
				return routeMatch{staticAt: -1}
			}
		case attr.Dynamic:
			if staticAt < 0 {
				staticAt = i
			}
		case attr.Text != segment.Text:
			return routeMatch{staticAt: -1}
		}
	}
	if len(attrSegs) != len(routeSegs) {
		return routeMatch{staticAt: -1}
	}
	return routeMatch{ok: staticAt < 0, staticAt: staticAt}
}

func methodAllows(route Route, method string) bool {
	return route.Method == method || route.Method == "*"
}

// routesFor returns the routes an attribute path reaches, method-compatible
// ones first.
func routesFor(url attributeURL, method string, routes []Route) (compatible, otherMethods []Route) {
	for _, route := range routes {
		if !matchRoute(url, route).ok {
			continue
		}
		if methodAllows(route, method) {
			compatible = append(compatible, route)
		} else {
			otherMethods = append(otherMethods, route)
		}
	}
	return compatible, otherMethods
}

type attributeProblem struct {
	code       string
	message    string
	start, end int
	severity   int
}

// diagnoseAttribute checks one attribute against the index.
func diagnoseAttribute(attribute htmxAttribute, index RouteIndex) []attributeProblem {
	url, ok := parseAttributeURL(attribute.Value, attribute.Literal)
	if !ok {
		return nil
	}
	compatible, otherMethods := routesFor(url, attribute.Method, index.Routes)
	if len(compatible) > 0 {
		return nil
	}
	whole := func(code, message string, severity int) []attributeProblem {
		return []attributeProblem{{code: code, message: message, start: attribute.ValueStart, end: attribute.ValueStart + len(url.Path), severity: severity}}
	}
	if len(otherMethods) > 0 {
		// A near miss with the attribute's own method explains more than a
		// literal match on another method's parameter.
		var sameMethod []Route
		for _, route := range index.Routes {
			if methodAllows(route, attribute.Method) {
				sameMethod = append(sameMethod, route)
			}
		}
		if problems := diagnoseParams(attribute, url, sameMethod); len(problems) > 0 {
			return problems
		}
		return whole("htmx-method-mismatch", fmt.Sprintf("No %s Chi route matches %s; it is registered for %s", attribute.Method, url.Path, methodList(otherMethods)), 1)
	}
	if problems := diagnoseParams(attribute, url, index.Routes); len(problems) > 0 {
		return problems
	}
	message := fmt.Sprintf("No Chi route matches %s", url.Path)
	if suggestion := closestRoute(url, attribute.Method, index.Routes); suggestion != "" {
		message += "; did you mean " + suggestion + "?"
	}
	switch count := len(index.Diagnostics); count {
	case 0:
	case 1:
		message += " (1 Chi registration could not be analysed)"
	default:
		message += fmt.Sprintf(" (%d Chi registrations could not be analysed)", count)
	}
	return whole("htmx-missing-route", message, 2)
}

func methodList(routes []Route) string {
	seen := make(map[string]bool)
	var methods []string
	for _, route := range routes {
		if !seen[route.Method] {
			seen[route.Method] = true
			methods = append(methods, route.Method)
		}
	}
	return strings.Join(methods, ", ")
}

// diagnoseParams explains near misses: routes with the same static segments
// whose parameters the attribute does not supply, supplies in excess, or
// interpolates where the route has a static segment.
func diagnoseParams(attribute htmxAttribute, url attributeURL, routes []Route) []attributeProblem {
	var best *Route
	var bestProblems []attributeProblem
	consider := func(route Route, problems []attributeProblem) {
		if len(problems) == 0 {
			return
		}
		if best == nil || methodAllows(route, attribute.Method) && !methodAllows(*best, attribute.Method) {
			copied := route
			best, bestProblems = &copied, problems
		}
	}
	for _, route := range routes {
		if route.Subtree {
			continue
		}
		routeSegs := routeSegments(route.Path)
		attrSegs := url.Segments
		if match := matchRoute(url, route); match.staticAt >= 0 {
			segment := attrSegs[match.staticAt]
			consider(route, []attributeProblem{{
				code:     "htmx-path-param-mismatch",
				message:  fmt.Sprintf("%s is interpolated where %s %s has the static segment %q", segment.Text, route.Method, route.Path, routeSegs[match.staticAt].Text),
				start:    attribute.ValueStart + segment.Start,
				end:      attribute.ValueStart + segment.End,
				severity: 1,
			}})
			continue
		}
		if !sameStatics(routeSegs, attrSegs) {
			continue
		}
		var problems []attributeProblem
		gapsRoute, gapsAttr := dynamicGaps(routeSegs), dynamicGaps(attrSegs)
		for gap := range gapsRoute {
			params, values := gapsRoute[gap], gapsAttr[gap]
			if len(values) < len(params) {
				var missing []string
				for _, param := range params[len(values):] {
					missing = append(missing, param.Text)
				}
				problems = append(problems, attributeProblem{
					code:     "htmx-missing-path-param",
					message:  fmt.Sprintf("%s is missing path parameter %s of %s %s", url.Path, strings.Join(missing, ", "), route.Method, route.Path),
					start:    attribute.ValueStart,
					end:      attribute.ValueStart + len(url.Path),
					severity: 1,
				})
			}
			for _, extra := range values[min(len(params), len(values)):] {
				problems = append(problems, attributeProblem{
					code:     "htmx-extra-path-param",
					message:  fmt.Sprintf("%s is not a path parameter of %s %s", extra.Text, route.Method, route.Path),
					start:    attribute.ValueStart + extra.Start,
					end:      attribute.ValueStart + extra.End,
					severity: 1,
				})
			}
		}
		consider(route, problems)
	}
	return bestProblems
}

func sameStatics(routeSegs, attrSegs []urlSegment) bool {
	var left, right []string
	for _, segment := range routeSegs {
		if !segment.Dynamic {
			left = append(left, segment.Text)
		}
	}
	for _, segment := range attrSegs {
		if !segment.Dynamic {
			right = append(right, segment.Text)
		}
	}
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// dynamicGaps groups dynamic segments by the number of static segments
// before them.
func dynamicGaps(segments []urlSegment) [][]urlSegment {
	gaps := [][]urlSegment{nil}
	for _, segment := range segments {
		if segment.Dynamic {
			gaps[len(gaps)-1] = append(gaps[len(gaps)-1], segment)
		} else {
			gaps = append(gaps, nil)
		}
	}
	return gaps
}

// rewriteAttribute renders newPath for an attribute that matched oldPath,
// keeping the attribute's values at parameter positions and its query.
func rewriteAttribute(url attributeURL, oldPath, newPath string) (string, bool) {
	oldSegs := routeSegments(oldPath)
	if len(url.Segments) != len(oldSegs) {
		return "", false
	}
	var values []string
	for i, segment := range oldSegs {
		if segment.Dynamic {
			values = append(values, url.Segments[i].Text)
		}
	}
	var builder strings.Builder
	next := 0
	for _, segment := range routeSegments(newPath) {
		builder.WriteByte('/')
		if segment.Dynamic {
			if next >= len(values) {
				return "", false
			}
			builder.WriteString(values[next])
			next++
			continue
		}
		builder.WriteString(segment.Text)
	}
	if next != len(values) {
		return "", false
	}
	return builder.String() + url.Suffix, true
}

// closestRoute suggests the method-compatible route whose static text is
// within a few edits of the attribute's, with as many segments and
// parameters compared as {}.
func closestRoute(url attributeURL, method string, routes []Route) string {
	shape := func(segments []urlSegment) string {
		var builder strings.Builder
		for _, segment := range segments {
			builder.WriteByte('/')
			if segment.Dynamic {
				builder.WriteString("{}")
			} else {
				builder.WriteString(segment.Text)
			}
		}
		return builder.String()
	}
	target := shape(url.Segments)
	best, bestDistance := "", 4
	for _, route := range routes {
		segments := routeSegments(route.Path)
		if !methodAllows(route, method) || route.Subtree || len(segments) != len(url.Segments) {
			continue
		}
		if distance := editDistance(target, shape(segments)); distance < bestDistance {
			best, bestDistance = route.Path, distance
		}
	}
	return best
}

func editDistance(left, right string) int {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(left); i++ {
		current[0] = i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}
