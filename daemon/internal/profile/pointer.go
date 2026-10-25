package profile

import "strings"

var (
	tokenEscaper   = strings.NewReplacer("~", "~0", "/", "~1")
	tokenUnescaper = strings.NewReplacer("~1", "/", "~0", "~")
)

// pointer returns the RFC 6901 JSON Pointer of a path of reference tokens
// (object keys and array indexes): "" for the root.
func pointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(tokenEscaper.Replace(t))
	}
	return b.String()
}

// childPointer returns the pointer of member key of the value at p.
func childPointer(p, key string) string {
	return p + "/" + tokenEscaper.Replace(key)
}

// splitPointer returns the reference tokens of JSON Pointer p. It reports
// false for a malformed pointer.
func splitPointer(p string) ([]string, bool) {
	if p == "" {
		return nil, true
	}
	if p[0] != '/' {
		return nil, false
	}
	tokens := strings.Split(p[1:], "/")
	for i, t := range tokens {
		for j := range len(t) {
			if t[j] == '~' && (j+1 == len(t) || t[j+1] != '0' && t[j+1] != '1') {
				return nil, false
			}
		}
		tokens[i] = tokenUnescaper.Replace(t)
	}
	return tokens, true
}
