package configdiag

import "strconv"

// Locate returns the byte range of the JSON value at path, or of its
// deepest existing ancestor (the key of a missing member is reported at its
// object). An unparseable prefix falls back to the first byte.
func Locate(source []byte, path []string) (start, end int) {
	l := locator{source: source}
	l.space()
	start, end = l.offset, l.offset
	for depth := 0; ; depth++ {
		valueStart := l.offset
		valueEnd, ok := l.skipValue()
		if !ok {
			break
		}
		start, end = valueStart, valueEnd
		if depth == len(path) {
			return start, end
		}
		l.offset = valueStart
		if !l.enter(path[depth]) {
			return start, end
		}
	}
	if end <= start {
		end = min(start+1, len(source))
	}
	return start, end
}

type locator struct {
	source []byte
	offset int
}

func (l *locator) space() {
	for l.offset < len(l.source) {
		switch l.source[l.offset] {
		case ' ', '\t', '\n', '\r':
			l.offset++
		default:
			return
		}
	}
}

// enter moves to the start of member key, or element index, of the object
// or array at the offset.
func (l *locator) enter(key string) bool {
	if l.offset >= len(l.source) {
		return false
	}
	switch l.source[l.offset] {
	case '{':
		l.offset++
		for {
			l.space()
			if l.offset >= len(l.source) || l.source[l.offset] != '"' {
				return false
			}
			nameStart := l.offset
			nameEnd, ok := l.skipString()
			if !ok {
				return false
			}
			name, err := strconv.Unquote(string(l.source[nameStart:nameEnd]))
			if err != nil {
				return false
			}
			l.space()
			if l.offset >= len(l.source) || l.source[l.offset] != ':' {
				return false
			}
			l.offset++
			l.space()
			if name == key {
				return true
			}
			if _, ok := l.skipValue(); !ok {
				return false
			}
			l.space()
			if l.offset >= len(l.source) || l.source[l.offset] != ',' {
				return false
			}
			l.offset++
		}
	case '[':
		index, err := strconv.Atoi(key)
		if err != nil || index < 0 {
			return false
		}
		l.offset++
		for current := 0; ; current++ {
			l.space()
			if current == index {
				return l.offset < len(l.source) && l.source[l.offset] != ']'
			}
			if _, ok := l.skipValue(); !ok {
				return false
			}
			l.space()
			if l.offset >= len(l.source) || l.source[l.offset] != ',' {
				return false
			}
			l.offset++
		}
	}
	return false
}

// skipValue moves past the value at the offset and returns its end.
func (l *locator) skipValue() (int, bool) {
	l.space()
	if l.offset >= len(l.source) {
		return 0, false
	}
	switch l.source[l.offset] {
	case '"':
		return l.skipString()
	case '{', '[':
		depth := 0
		for l.offset < len(l.source) {
			switch l.source[l.offset] {
			case '"':
				if _, ok := l.skipString(); !ok {
					return 0, false
				}
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					l.offset++
					return l.offset, true
				}
			}
			l.offset++
		}
		return 0, false
	}
	start := l.offset
	for l.offset < len(l.source) {
		switch l.source[l.offset] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return l.offset, l.offset > start
		}
		l.offset++
	}
	return l.offset, l.offset > start
}

// skipString moves past the string at the offset and returns its end.
func (l *locator) skipString() (int, bool) {
	l.offset++
	for l.offset < len(l.source) {
		switch l.source[l.offset] {
		case '\\':
			l.offset += 2
			continue
		case '"':
			l.offset++
			return l.offset, true
		}
		l.offset++
	}
	return 0, false
}
