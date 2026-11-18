package picker

import (
	"errors"
	"strings"
)

// splitWords splits an editor variable such as $EDITOR into words as a
// POSIX shell would, without running one: blanks separate words, single
// quotes keep everything up to the next single quote, double quotes keep
// everything but a backslash before ", \, $, or `, and a backslash outside
// quotes keeps the next character. Nothing is expanded, so a value that
// needs a shell (an unquoted $, `, |, &, ;, <, >, (, or ), or an unclosed
// quote) is an error rather than something run in a way its author did not
// mean.
func splitWords(value string) ([]string, error) {
	var words []string
	var word strings.Builder
	inWord := false
	runes := []rune(value)
	for index := 0; index < len(runes); index++ {
		r := runes[index]
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		case r == '\'':
			inWord = true
			end := index + 1
			for end < len(runes) && runes[end] != '\'' {
				end++
			}
			if end == len(runes) {
				return nil, errors.New("it has an unclosed single quote")
			}
			word.WriteString(string(runes[index+1 : end]))
			index = end
		case r == '"':
			inWord = true
			index++
			for ; index < len(runes) && runes[index] != '"'; index++ {
				if runes[index] == '\\' && index+1 < len(runes) && strings.ContainsRune("\"\\$`", runes[index+1]) {
					index++
				} else if runes[index] == '$' || runes[index] == '`' {
					return nil, errors.New("it needs a shell to expand " + string(runes[index]))
				}
				word.WriteRune(runes[index])
			}
			if index == len(runes) {
				return nil, errors.New("it has an unclosed double quote")
			}
		case r == '\\':
			inWord = true
			if index+1 == len(runes) {
				return nil, errors.New("it ends with a backslash")
			}
			index++
			word.WriteRune(runes[index])
		case strings.ContainsRune("$`|&;<>()", r):
			return nil, errors.New("it needs a shell to interpret " + string(r))
		default:
			inWord = true
			word.WriteRune(r)
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	if len(words) == 0 || words[0] == "" {
		return nil, errors.New("it names no program")
	}
	return words, nil
}
