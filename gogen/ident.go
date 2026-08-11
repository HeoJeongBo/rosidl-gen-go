package gogen

import (
	"fmt"
	"go/format"
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
// emitted; on failure the offending source is included so the syntax error can
// be found without writing anything to disk.
func FormatFile(name string, src []byte) ([]byte, error) {
	formatted, err := format.Source(src)
	if err != nil {
		return nil, fmt.Errorf("gofmt %s: %w\n%s", name, err, src)
	}
	return formatted, nil
}
