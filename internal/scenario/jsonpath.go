package scenario

import (
	"fmt"
	"strconv"
	"strings"
)

// JSONPathToGJSON converts the JSONPath subset Stampede accepts into a
// gjson path. Supported: $, .key, ['key'], ["key"], [n], [*] and .length.
//
//	$.items[0].id      -> items.0.id
//	$.items[*].id      -> items.#.id
//	$['odd key'].x     -> odd key.x (escaped)
//	$.items.length     -> items.#
func JSONPathToGJSON(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "$" {
		return "@this", nil
	}
	if !strings.HasPrefix(p, "$") {
		return "", fmt.Errorf("JSONPath %q must start with $", p)
	}
	var parts []string
	i := 1
	for i < len(p) {
		switch p[i] {
		case '.':
			i++
			j := i
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			key := p[i:j]
			if key == "" {
				return "", fmt.Errorf("JSONPath %q has an empty key", p)
			}
			if key == "length" && j == len(p) {
				parts = append(parts, "#")
			} else {
				parts = append(parts, escapeGJSON(key))
			}
			i = j
		case '[':
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				return "", fmt.Errorf("JSONPath %q has an unclosed [", p)
			}
			inner := strings.TrimSpace(p[i+1 : i+end])
			i += end + 1
			switch {
			case inner == "*":
				parts = append(parts, "#")
			case len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0]:
				parts = append(parts, escapeGJSON(inner[1:len(inner)-1]))
			default:
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return "", fmt.Errorf("JSONPath %q: unsupported index [%s]", p, inner)
				}
				parts = append(parts, strconv.Itoa(n))
			}
		default:
			return "", fmt.Errorf("JSONPath %q: unexpected %q at position %d", p, p[i], i)
		}
	}
	if len(parts) == 0 {
		return "@this", nil
	}
	return strings.Join(parts, "."), nil
}

func escapeGJSON(key string) string {
	var b strings.Builder
	for _, c := range key {
		switch c {
		case '.', '*', '?', '|', '#', '@', '\\', '!', '=', '<', '>', '%':
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}
