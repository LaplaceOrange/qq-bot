package bot

import (
	"errors"

	"strings"

	"unicode"
)

func splitConfigArgs(content string) ([]string, error) {
	runes := []rune(strings.TrimSpace(content))
	var args []string
	for i := 0; i < len(runes); {
		if unicode.IsSpace(runes[i]) {
			i++
			continue
		}
		if runes[i] == '[' || runes[i] == '{' {
			args = append(args, strings.TrimSpace(string(runes[i:])))
			break
		}
		var value strings.Builder
		quote := rune(0)
		if runes[i] == '\'' || runes[i] == '"' {
			quote = runes[i]
			i++
		}
		closed := quote == 0
		for i < len(runes) {
			char := runes[i]
			if quote == 0 && unicode.IsSpace(char) {
				break
			}
			if quote != 0 && char == quote {
				i++
				closed = true
				if i < len(runes) && !unicode.IsSpace(runes[i]) {
					return nil, errors.New("quoted value must end at whitespace")
				}
				break
			}
			if quote != 0 && char == '\\' && i+1 < len(runes) && runes[i+1] == quote {
				i++
				char = runes[i]
			}
			value.WriteRune(char)
			i++
		}
		if !closed {
			return nil, errors.New("unterminated quote")
		}
		args = append(args, value.String())
	}
	if len(args) == 0 {
		return nil, errors.New("empty command")
	}
	return args, nil
}
