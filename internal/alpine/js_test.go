package alpine_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/alpine"
)

func TestParseProgram_GIVENAlpineExpressions_WHENParsed_THENAccepts(t *testing.T) {
	valid := []string{
		`{ open: false, toggle() { this.open = !this.open } }`,
		"{\n\tselected: '', selectedStart: -1,\n\tselectCriterion(id, start, end) {\n\t\tthis.selected = id; this.$nextTick(() => { this.$refs.x.scrollIntoView({ block: 'nearest' }) })\n\t},\n\tget count() { return this.items.length },\n}",
		`open = !open`,
		`editing = !editing; if(editing) $dispatch('cancel-edits', id)`,
		`editing ? 'Cancel' : 'Unlock'`,
		`selected = ''; selectedStart = -1; selectedEnd = -1`,
		`let x = 1; foo(x)`,
		`items.filter(i => i.done).length > 0`,
		`async () => { await fetch('/x'); done = true }`,
		"`${count} items`",
		`a?.b?.[c]?.(d) ?? 'none'`,
		`/^\d+$/.test(value)`,
		`x = y / 2 / z`,
		`{ ...defaults, open: true }`,
		`[1, , 3]`,
		`$watch('open', value => console.log(value))`,
		`new Date().getFullYear()`,
		`({ a, b = 2 } = obj)`,
		`for (const item of items) { total += item.price }`,
		`try { go() } catch (e) { error = e.message } finally { busy = false }`,
		`function named(a, ...rest) { return rest }`,
		`count++`,
		`2 ** 3 ** 2`,
		`typeof x === 'undefined' || x instanceof Y`,
		`'k' in obj`,
		`{ "quoted key": 1, 2: 'two', [computed]: 3, async load() {}, *gen() {} }`,
		"open = false\nclose()",
		"\x00 && open",
		"{ open: \x00 }",
		`selectCriterion(` + "\x00, \x00, \x00" + `)`,
	}
	for _, source := range valid {
		if _, err := alpine.ParseProgram(source); err != nil {
			t.Errorf("ParseProgram(%q) = %v", source, err)
		}
	}
}

func TestParseProgram_GIVENInvalidExpressions_WHENParsed_THENReportsTheOffset(t *testing.T) {
	tests := []struct {
		source string
		start  int
	}{
		{`open = `, 7},
		{`toggle(`, 7},
		{`{ open: false`, 13},
		{`a b`, 2},
		{`'unterminated`, 0},
		{`1 = 2`, 0},
		{`items.`, 6},
		{`class X {}`, 0},
	}
	for _, test := range tests {
		_, err := alpine.ParseProgram(test.source)
		if err == nil {
			t.Errorf("ParseProgram(%q) accepted", test.source)
			continue
		}
		if err.Start != test.start {
			t.Errorf("ParseProgram(%q) error at %d (%s), want %d", test.source, err.Start, err.Message, test.start)
		}
	}
}
