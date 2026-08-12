package gogen

import (
	"fmt"
	"go/format"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Pascal converts a ROS snake_case field or SCREAMING_SNAKE constant name to a
// Go exported identifier. The transform is deliberately mechanical (no
// initialism table): `config_json` -> ConfigJson, `http_url` -> HttpUrl. It can
// differ from a hand-written mirror that prettified an embedded camelCase —
// `mesh_uvindex` is MeshUvindex here where a hand mirror may spell MeshUvIndex —
// and the mechanical form wins, because it round-trips from the .msg name.
func Pascal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, part := range strings.Split(s, "_") {
		if part == "" {
			continue
		}
		r := []rune(strings.ToLower(part))
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// CommentBlock renders doc lines as a Go comment block indented by indent.
// Empty input yields nothing so the caller can concatenate unconditionally.
func CommentBlock(indent string, lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(indent)
		if l == "" {
			b.WriteString("//\n")
			continue
		}
		b.WriteString("// ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

// FormatFile runs gofmt over generated source. name identifies the file being
// emitted; on failure a few lines around the reported position are included so
// the syntax error can be located without writing anything to disk.
//
// Note that this only parses. It does not type-check, so it cannot catch a
// duplicate declaration — [Generator.Resolve] claims identifiers for that.
func FormatFile(name string, src []byte) ([]byte, error) {
	formatted, err := format.Source(src)
	if err != nil {
		return nil, fmt.Errorf("gofmt %s: %w\n%s", name, err, excerpt(src, err))
	}
	return formatted, nil
}

// excerpt renders the lines around the position named in a go/format error,
// indented. Dumping the whole file here would bury the message under thousands
// of lines of generated source for a real interface set.
func excerpt(src []byte, err error) string {
	const context = 3

	// go/format errors are "<file>:<line>:<col>: message"; take the first line
	// number mentioned and fall back to the head of the file.
	center := 1
	if m := errLineRe.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			center = n
		}
	}

	lines := strings.Split(string(src), "\n")
	lo := max(center-context, 1)
	hi := min(center+context, len(lines))

	var b strings.Builder
	for i := lo; i <= hi; i++ {
		marker := "  "
		if i == center {
			marker = "->"
		}
		fmt.Fprintf(&b, "\t%s %4d | %s\n", marker, i, lines[i-1])
	}
	return b.String()
}

var errLineRe = regexp.MustCompile(`:(\d+):\d+:`)
