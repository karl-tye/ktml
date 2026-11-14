package compiler_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

// positionsOf splits a script on {} placeholders and returns the position
// of each interpolation.
func positionsOf(script string) []compiler.ScriptPosition {
	parts := strings.Split(script, "{}")
	var literals []string
	var isPath []bool
	for index, part := range parts {
		literals = append(literals, part)
		isPath = append(isPath, false)
		if index < len(parts)-1 {
			literals = append(literals, "")
			isPath = append(isPath, true)
		}
	}
	var positions []compiler.ScriptPosition
	for index, position := range compiler.ScriptInterpolations(literals, isPath) {
		if isPath[index] {
			positions = append(positions, position)
		}
	}
	return positions
}

func TestScriptInterpolations_GIVENStaticJavaScript_WHENInterpolated_THENClassifiesEachPosition(t *testing.T) {
	tests := []struct {
		script string
		want   compiler.ScriptPosition
	}{
		// Accepted: expression positions.
		{`createChart({}, {})`, compiler.ScriptExpression},
		{`const config = {};`, compiler.ScriptExpression},
		{`window.cfg = { a: {}, b: [{}, 2] };`, compiler.ScriptExpression},
		{`if (a) { go({}); }`, compiler.ScriptExpression},
		{`x = "a\"" + {}`, compiler.ScriptExpression},
		{`x = 'it\'s' + {} // done`, compiler.ScriptExpression},
		{`/* c */ f({})`, compiler.ScriptExpression},
		{"// c\nf({})", compiler.ScriptExpression},
		{`x = a / b; y = {}`, compiler.ScriptExpression},
		{`x = /re/g.test(s) && {}`, compiler.ScriptExpression},
		{"x = `t` + {}", compiler.ScriptExpression},
		{"x = `${a}` + {}", compiler.ScriptExpression},
		{`return {} / 2`, compiler.ScriptExpression},
		// Rejected: inside strings.
		{`go('{}')`, compiler.ScriptString},
		{`go("a {} b")`, compiler.ScriptString},
		{`go("a\" {}")`, compiler.ScriptString},
		// Rejected: template literals, including substitutions.
		{"go(`{}`)", compiler.ScriptTemplate},
		{"go(`a ${ {} } b`)", compiler.ScriptTemplate},
		{"go(`a ${ f({}) }`)", compiler.ScriptTemplate},
		{"go(`${ {a: 1} } {}`)", compiler.ScriptTemplate},
		// Rejected: comments.
		{`go() // {}`, compiler.ScriptComment},
		{`go() /* {} */`, compiler.ScriptComment},
		// Rejected: regular expressions.
		{`x = /a{}b/`, compiler.ScriptRegex},
		{`x = s.match(/[/]{}/)`, compiler.ScriptRegex},
		{`return /{}/`, compiler.ScriptRegex},
		// Rejected: a / the lexer cannot classify.
		{`if (a) /x/.test(b); go({})`, compiler.ScriptUncertain},
		{`{ } /x/; go({})`, compiler.ScriptUncertain},
		{`(a + b) / 2; go({})`, compiler.ScriptUncertain},
	}
	for _, test := range tests {
		t.Run(test.script, func(t *testing.T) {
			positions := positionsOf(test.script)
			if len(positions) == 0 {
				t.Fatal("no interpolation")
			}
			got := positions[len(positions)-1]
			for _, position := range positions {
				if position != compiler.ScriptExpression {
					got = position
					break
				}
			}
			if got != test.want {
				t.Fatalf("position = %s, want %s", got.Describe(), test.want.Describe())
			}
		})
	}
}
