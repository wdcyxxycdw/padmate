// Package dtsgen filters the padmate TypeScript declarations for a script API
// version and writes the small editor scaffold used by VS Code and Monaco:
// a legacy script's project (Project) and the macros folder's (MacroProject,
// whose padmate module keeps only the values marked @macro).
package dtsgen

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

//go:embed padmate.d.ts
var defaultDeclarations []byte

// Version is a script API major/minor version.
type Version struct{ Major, Minor int }

// HostAPI is the current script API, the first of SupportedAPIs.
var HostAPI = SupportedAPIs[0]

// ValidateHostAPI accepts the script API versions of SupportedAPIs (the
// current one and the previous 0.N) and refuses the others.
func ValidateHostAPI(api Version) error {
	if !Supported(api) {
		return errUnsupported(api)
	}
	return nil
}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }
func (v Version) before(other Version) bool {
	return v.Major < other.Major || v.Major == other.Major && v.Minor < other.Minor
}

var versionPattern = regexp.MustCompile(`^(?:v)?([0-9]+)\.([0-9]+)$`)
var sincePattern = regexp.MustCompile(`@since\s+([vV]?[0-9]+\.[0-9]+)\b`)
var untilPattern = regexp.MustCompile(`@until\s+([vV]?[0-9]+\.[0-9]+)\b`)

// ParseVersion parses the strict major.minor format used by script.api.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("invalid API version %q (want major.minor)", s)
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid API version %q: major is out of range: %w", s, err)
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return Version{}, fmt.Errorf("invalid API version %q: minor is out of range: %w", s, err)
	}
	return Version{Major: major, Minor: minor}, nil
}

// Filter removes declarations and members whose @since version is newer than
// api. It preserves comments, whitespace, and all declarations that are
// available to that API. A malformed @since annotation is an error.
func Filter(src []byte, api Version) ([]byte, error) {
	s, err := filter(string(src), api)
	return []byte(s), err
}
func FilterString(src string, api Version) (string, error) {
	b, err := Filter([]byte(src), api)
	return string(b), err
}

func filter(src string, api Version) (string, error) {
	lines := strings.SplitAfter(src, "\n")
	var out strings.Builder
	for i := 0; i < len(lines); {
		if !strings.Contains(lines[i], "/**") {
			out.WriteString(lines[i])
			i++
			continue
		}
		end := i
		for !strings.Contains(lines[end], "*/") && end+1 < len(lines) {
			end++
		}
		if !strings.Contains(lines[end], "*/") {
			return "", fmt.Errorf("unterminated JSDoc at line %d", i+1)
		}
		comment := strings.Join(lines[i:end+1], "")
		since, hasSince, err := lineSince(comment)
		if err != nil {
			return "", err
		}
		if hasSince && api.before(since) {
			i = skipDeclaration(lines, i)
			continue
		}
		if strings.Contains(comment, "@until") {
			m := untilPattern.FindStringSubmatch(comment)
			if m == nil {
				return "", fmt.Errorf("invalid @until annotation in %q", strings.TrimSpace(comment))
			}
			until, err := ParseVersion(m[1])
			if err != nil {
				return "", err
			}
			if !api.before(until) {
				i = skipDeclaration(lines, i)
				continue
			}
		}
		for ; i <= end; i++ {
			out.WriteString(lines[i])
		}
	}
	return out.String(), nil
}

func lineSince(line string) (Version, bool, error) {
	m := sincePattern.FindStringSubmatch(line)
	if m == nil {
		if strings.Contains(line, "@since") {
			return Version{}, true, fmt.Errorf("invalid @since annotation in %q", strings.TrimSpace(line))
		}
		return Version{}, false, nil
	}
	v, err := ParseVersion(m[1])
	if err != nil {
		return Version{}, true, err
	}
	return v, true, nil
}

// skipDeclaration skips one declaration starting at start. Braces are enough
// for interfaces, namespaces, and object-shaped declarations; semicolon
// termination handles functions, fields, and type aliases.
func skipDeclaration(lines []string, start int) int {
	if start >= len(lines) {
		return start + 1
	}
	depth := 0
	sawBrace := false
	paren := 0
	blockComment := false
	for i := start; i < len(lines); i++ {
		line, nextBlock := stripStringsAndComments(lines[i], blockComment)
		blockComment = nextBlock
		for _, r := range line {
			switch r {
			case '{':
				depth++
				sawBrace = true
			case '}':
				if depth > 0 {
					depth--
				}
			case '(':
				paren++
			case ')':
				if paren > 0 {
					paren--
				}
			}
		}
		if sawBrace && depth == 0 && paren == 0 {
			return i + 1
		}
		if !sawBrace && paren == 0 && (strings.Contains(line, ";") || strings.Contains(line, "}")) {
			return i + 1
		}
	}
	return len(lines)
}

func stripStringsAndComments(s string, blockComment bool) (string, bool) {
	var b strings.Builder
	quote := rune(0)
	escaped := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if blockComment {
			if r == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' || r == '`' {
			quote = r
			continue
		}
		if r == '/' && i+1 < len(runes) {
			switch runes[i+1] {
			case '*':
				blockComment = true
				i++
				continue
			case '/':
				return b.String(), blockComment
			}
		}
		b.WriteRune(r)
	}
	return b.String(), blockComment
}
