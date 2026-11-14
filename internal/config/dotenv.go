package config

import (
	"fmt"
	"regexp"
	"strings"
)

// EnvVar is one variable of the application's environment, and where it
// was set: "<env file>:<line>" or "dev.env".
type EnvVar struct {
	Key    string
	Value  string
	Source string
	// Line is the variable's line in its env file; 0 for dev.env.
	Line int
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ControlEnvPrefix names the variables ktml dev sets for the application's
// development control channel and story capture. An env file or dev.env
// never sets them.
const ControlEnvPrefix = "KTML_DEV_"

// ParseEnvFile reads an env file as `set -a; . ./.env` would set it: KEY=value
// lines, optionally after export, with sh quoting ('literal', "with \" \\ \$
// escapes"), backslash escapes, and $NAME, ${NAME}, and ${NAME:-default}
// expansion of the file's earlier variables, then of lookup. Blank lines and
// # comments are skipped. Anything that would run a command, such as $(...)
// or backticks, is refused. Errors name the line, never a value. Variables
// are returned in file order; a repeated key keeps its last value.
func ParseEnvFile(data []byte, lookup func(string) (string, bool)) ([]EnvVar, error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	p := &envParser{text: strings.ReplaceAll(string(data), "\r\n", "\n"), line: 1, values: make(map[string]string), lookup: lookup}
	var vars []EnvVar
	index := make(map[string]int)
	for {
		p.skipBlank()
		if p.done() {
			return vars, nil
		}
		line := p.line
		if p.peek() == '#' {
			p.skipLine()
			continue
		}
		name := p.word()
		if name == "export" && (p.peek() == ' ' || p.peek() == '\t') {
			p.skipSpaces()
			name = p.word()
			if p.done() || p.peek() == '\n' || p.peek() == '#' || p.peek() == ' ' || p.peek() == '\t' {
				// export NAME marks a variable; set -a already exports.
				if err := p.endOfLine(line); err != nil {
					return nil, err
				}
				if !envName.MatchString(name) {
					return nil, fmt.Errorf("line %d: expected NAME=value", line)
				}
				continue
			}
		}
		if !envName.MatchString(name) || p.peek() != '=' {
			return nil, fmt.Errorf("line %d: expected NAME=value", line)
		}
		p.pos++
		value, err := p.value(line)
		if err != nil {
			return nil, err
		}
		if err := p.endOfLine(line); err != nil {
			return nil, err
		}
		p.values[name] = value
		if at, seen := index[name]; seen {
			vars[at] = EnvVar{Key: name, Value: value, Line: line}
			continue
		}
		index[name] = len(vars)
		vars = append(vars, EnvVar{Key: name, Value: value, Line: line})
	}
}

type envParser struct {
	text   string
	pos    int
	line   int
	values map[string]string
	lookup func(string) (string, bool)
}

func (p *envParser) done() bool { return p.pos >= len(p.text) }

func (p *envParser) peek() byte {
	if p.done() {
		return 0
	}
	return p.text[p.pos]
}

func (p *envParser) next() byte {
	c := p.text[p.pos]
	p.pos++
	if c == '\n' {
		p.line++
	}
	return c
}

func (p *envParser) skipBlank() {
	for !p.done() && strings.IndexByte(" \t\n", p.peek()) >= 0 {
		p.next()
	}
}

func (p *envParser) skipSpaces() {
	for p.peek() == ' ' || p.peek() == '\t' {
		p.pos++
	}
}

func (p *envParser) skipLine() {
	for !p.done() && p.peek() != '\n' {
		p.pos++
	}
}

func (p *envParser) word() string {
	start := p.pos
	for !p.done() && (p.peek() == '_' || p.peek() >= 'a' && p.peek() <= 'z' || p.peek() >= 'A' && p.peek() <= 'Z' || p.peek() >= '0' && p.peek() <= '9') {
		p.pos++
	}
	return p.text[start:p.pos]
}

// endOfLine accepts trailing blanks and a comment.
func (p *envParser) endOfLine(line int) error {
	p.skipSpaces()
	switch {
	case p.done() || p.peek() == '\n':
		return nil
	case p.peek() == '#':
		p.skipLine()
		return nil
	}
	return fmt.Errorf("line %d: unquoted whitespace in a value; quote the whole value", line)
}

// value reads one sh word: unquoted, single-quoted, and double-quoted parts
// up to unquoted whitespace or the end of the line.
func (p *envParser) value(line int) (string, error) {
	var b strings.Builder
	for !p.done() {
		c := p.peek()
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			return b.String(), nil
		case c == '\'':
			p.pos++
			end := strings.IndexByte(p.text[p.pos:], '\'')
			if end < 0 {
				return "", fmt.Errorf("line %d: unterminated single quote", line)
			}
			literal := p.text[p.pos : p.pos+end]
			b.WriteString(literal)
			p.line += strings.Count(literal, "\n")
			p.pos += end + 1
		case c == '"':
			p.pos++
			if err := p.double(&b, line); err != nil {
				return "", err
			}
		case c == '\\':
			p.pos++
			if p.done() {
				return b.String(), nil
			}
			if p.peek() == '\n' {
				p.next()
				continue
			}
			b.WriteByte(p.next())
		case c == '$':
			if err := p.expand(&b, line); err != nil {
				return "", err
			}
		case c == '`':
			return "", fmt.Errorf("line %d: command substitution is not supported", line)
		default:
			b.WriteByte(p.next())
		}
	}
	return b.String(), nil
}

// double reads the rest of a double-quoted part, where a backslash escapes
// only $ ` " \ and a newline.
func (p *envParser) double(b *strings.Builder, line int) error {
	for !p.done() {
		c := p.peek()
		switch c {
		case '"':
			p.pos++
			return nil
		case '\\':
			p.pos++
			if p.done() {
				return fmt.Errorf("line %d: unterminated double quote", line)
			}
			switch p.peek() {
			case '$', '`', '"', '\\':
				b.WriteByte(p.next())
			case '\n':
				p.next()
			default:
				b.WriteByte('\\')
			}
		case '$':
			if err := p.expand(b, line); err != nil {
				return err
			}
		case '`':
			return fmt.Errorf("line %d: command substitution is not supported", line)
		default:
			b.WriteByte(p.next())
		}
	}
	return fmt.Errorf("line %d: unterminated double quote", line)
}

// expand writes the value of $NAME, ${NAME}, or ${NAME:-default}. A $ before
// anything else is literal.
func (p *envParser) expand(b *strings.Builder, line int) error {
	p.pos++
	switch {
	case p.peek() == '(':
		return fmt.Errorf("line %d: command substitution is not supported", line)
	case p.peek() == '{':
		p.pos++
		name := p.word()
		if !envName.MatchString(name) {
			return fmt.Errorf("line %d: unsupported ${...} expansion", line)
		}
		value, set := p.get(name)
		switch {
		case p.peek() == '}':
			p.pos++
		case strings.HasPrefix(p.text[p.pos:], ":-") || p.peek() == '-':
			colon := p.peek() == ':'
			if colon {
				p.pos++
			}
			p.pos++
			end := strings.IndexByte(p.text[p.pos:], '}')
			if end < 0 || strings.ContainsAny(p.text[p.pos:p.pos+end], "$`'\"\\\n") {
				return fmt.Errorf("line %d: unsupported ${...} expansion", line)
			}
			fallback := p.text[p.pos : p.pos+end]
			p.pos += end + 1
			if !set || colon && value == "" {
				value = fallback
			}
		default:
			return fmt.Errorf("line %d: unsupported ${...} expansion", line)
		}
		b.WriteString(value)
	default:
		name := p.word()
		if name == "" || name[0] >= '0' && name[0] <= '9' {
			b.WriteByte('$')
			b.WriteString(name)
			return nil
		}
		value, _ := p.get(name)
		b.WriteString(value)
	}
	return nil
}

func (p *envParser) get(name string) (string, bool) {
	if value, ok := p.values[name]; ok {
		return value, true
	}
	return p.lookup(name)
}
