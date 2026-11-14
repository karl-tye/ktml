package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseEnvFile_GIVENShellDotenvSyntax_WHENParsed_THENMatchesWhatShWouldSet(t *testing.T) {
	source := strings.Join([]string{
		"# comment",
		"",
		"PLAIN=value",
		"export EXPORTED=yes",
		"SINGLE='a $b \\n c'",
		`DOUBLE="x \"y\" \$z \n"`,
		"EMPTY=",
		"EXPANDED=${PLAIN}-$EXPORTED",
		"FROM_PROCESS=$HOME/x",
		"UNSET=[$MISSING]",
		"DEFAULTED=${MISSING:-fallback}",
		"TRAILING=word # a comment",
		"HASH=a#b",
		"JOINED=a'b c'\"d\"",
		"MULTI=\"line one",
		"line two\"",
		"ESCAPED=a\\ b",
		"  INDENTED=1",
		"export ONLY_EXPORT",
	}, "\n") + "\n"
	lookup := func(key string) (string, bool) {
		if key == "HOME" {
			return "/home/me", true
		}
		return "", false
	}
	got, err := ParseEnvFile([]byte(source), lookup)
	if err != nil {
		t.Fatalf("ParseEnvFile() error = %v", err)
	}
	want := map[string]string{
		"PLAIN":        "value",
		"EXPORTED":     "yes",
		"SINGLE":       `a $b \n c`,
		"DOUBLE":       `x "y" $z \n`,
		"EMPTY":        "",
		"EXPANDED":     "value-yes",
		"FROM_PROCESS": "/home/me/x",
		"UNSET":        "[]",
		"DEFAULTED":    "fallback",
		"TRAILING":     "word",
		"HASH":         "a#b",
		"JOINED":       "ab cd",
		"MULTI":        "line one\nline two",
		"ESCAPED":      "a b",
		"INDENTED":     "1",
	}
	values := make(map[string]string)
	var order []string
	for _, variable := range got {
		values[variable.Key] = variable.Value
		order = append(order, variable.Key)
	}
	if !reflect.DeepEqual(values, want) {
		t.Errorf("ParseEnvFile() = %v\nwant %v", values, want)
	}
	if order[0] != "PLAIN" || got[0].Line != 3 || got[len(got)-1].Key != "INDENTED" {
		t.Errorf("order = %v, first line %d; want file order with line numbers", order, got[0].Line)
	}
}

func TestParseEnvFile_GIVENUnsupportedShell_WHENParsed_THENFailsWithoutTheValue(t *testing.T) {
	for _, line := range []string{
		"CMD=$(cat /etc/secret-value)",
		"TICK=`whoami-secret-value`",
		"SPACED=secret-value other",
		"OPEN='secret-value",
		"OPEN2=\"secret-value",
		"not a variable secret-value",
		"WEIRD=${secret-value",
		"ODD=${X:?secret-value}",
	} {
		_, err := ParseEnvFile([]byte("OK=1\n"+line+"\n"), nil)
		if err == nil {
			t.Errorf("%q: no error", line)
			continue
		}
		if strings.Contains(err.Error(), "secret-value") {
			t.Errorf("%q: the error prints the value: %v", line, err)
		}
		if !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%q: error %v does not name line 2", line, err)
		}
	}
}
