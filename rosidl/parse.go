package rosidl

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// EmptyMemberField is the dummy member rosidl adds to an otherwise empty
// message so the middleware has something to serialize. Cyclone DDS rejects
// zero-field structs outright, so a service with no request fields still
// carries this one byte on the wire.
const EmptyMemberField = "structure_needs_at_least_one_member"

// Const is a `<type> <NAME>=<value>` declaration.
type Const struct {
	Name  string
	Type  Type
	Value string
	Doc   []string
}

// Field is a `<type> <name> [default]` declaration. Declaration order is the
// CDR wire order and must be preserved.
type Field struct {
	Name    string
	Type    Type
	Default string
	Doc     []string
}

// Message is a parsed .msg, or one half of a parsed .srv.
type Message struct {
	Name   Name
	Path   string
	Doc    []string
	Consts []Const
	Fields []Field
}

// Service is a parsed .srv.
type Service struct {
	Name     Name
	Path     string
	Doc      []string
	Request  Message
	Response Message
}

// ParseMessage parses the body of a .msg file. name supplies the package used
// to resolve unqualified nested references. A body with no fields — a
// constants-only message, or a truly empty one — gets the EmptyMemberField
// dummy, matching rosidl's generated C struct.
func ParseMessage(name Name, path, body string) (Message, error) {
	m := Message{Name: name, Path: path}
	doc, consts, fields, err := parseBody(name.Package, body)
	if err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	m.Doc, m.Consts, m.Fields = doc, consts, fields
	fillEmpty(&m)
	return m, nil
}

// ParseService parses a .srv file, splitting it on the `---` separator into the
// implicit `<Type>_Request` and `<Type>_Response` messages. Both halves go
// through ParseMessage, so an empty one already carries the EmptyMemberField
// dummy.
func ParseService(name Name, path, body string) (Service, error) {
	s := Service{Name: name, Path: path}

	req, res, ok := splitService(body)
	if !ok {
		return s, fmt.Errorf("%s: missing `---` request/response separator", path)
	}

	var err error
	if s.Request, err = ParseMessage(name.RequestName(), path, req); err != nil {
		return s, err
	}
	if s.Response, err = ParseMessage(name.ResponseName(), path, res); err != nil {
		return s, err
	}

	// The leading comment block of the file documents the service, not its
	// request; hoist it so both halves are not tagged with it.
	s.Doc, s.Request.Doc = s.Request.Doc, nil
	return s, nil
}

func fillEmpty(m *Message) {
	if len(m.Fields) > 0 {
		return
	}
	m.Fields = []Field{{
		Name: EmptyMemberField,
		Type: Type{Primitive: "uint8"},
		Doc:  []string{"Dummy member rosidl adds to an empty message; ignore it."},
	}}
}

func splitService(body string) (req, res string, ok bool) {
	var (
		reqLines []string
		resLines []string
		seen     bool
	)
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !seen && strings.TrimSpace(line) == "---" {
			seen = true
			continue
		}
		if seen {
			resLines = append(resLines, line)
		} else {
			reqLines = append(reqLines, line)
		}
	}
	return strings.Join(reqLines, "\n"), strings.Join(resLines, "\n"), seen
}

// markerCommentRe matches a `# Request` / `# Response` section marker. Those
// separate the halves of a .srv for a human reader and carry no information
// once the halves are distinct Go types.
var markerCommentRe = regexp.MustCompile(`^(?i)(request|response)(\s*\(.*\))?$`)

func parseBody(pkg, body string) (doc []string, consts []Const, fields []Field, err error) {
	lines := strings.Split(body, "\n")
	lineNoErr := func(n int, e error) error { return fmt.Errorf("line %d: %w", n, e) }

	// The comment block opening the file documents the message. Take it before
	// the field loop so it is not mistaken for documentation of the first field,
	// which is what happens when no blank line separates the two.
	i := 0
	for ; i < len(lines); i++ {
		code, comment := splitComment(lines[i])
		if strings.TrimSpace(code) != "" || comment == "" {
			break
		}
		doc = append(doc, comment)
	}
	if len(doc) == 1 && markerCommentRe.MatchString(doc[0]) {
		doc = nil
	}

	var pending []string
	for ; i < len(lines); i++ {
		n := i + 1
		code, comment := splitComment(lines[i])
		code = strings.TrimSpace(code)

		if code == "" {
			if comment != "" {
				pending = append(pending, comment)
			} else {
				// A blank line ends a comment block; anything accumulated so far
				// documents nothing and must not attach to a later field.
				pending = nil
			}
			continue
		}

		d := pending
		pending = nil
		if comment != "" {
			d = append(d, comment)
		}

		typeStr, rest := cutSpace(code)
		if rest == "" {
			return nil, nil, nil, lineNoErr(n, fmt.Errorf("declaration %q has no name", code))
		}

		t, e := parseType(typeStr, pkg)
		if e != nil {
			return nil, nil, nil, lineNoErr(n, e)
		}

		if name, value, isConst := cutConstant(rest); isConst {
			if t.Array != ArrayNone {
				return nil, nil, nil, lineNoErr(n, fmt.Errorf("array constant %q is not supported", code))
			}
			consts = append(consts, Const{Name: name, Type: t, Value: value, Doc: d})
			continue
		}

		name, def := cutSpace(rest)
		fields = append(fields, Field{Name: name, Type: t, Default: def, Doc: d})
	}
	return doc, consts, fields, nil
}

// cutSpace splits at the first whitespace RUN and trims the remainder. Tabs are
// legal separators in a .msg; cutting on the space character alone misreads
// `uint8\tfoo` as a nameless declaration and `uint8 foo\t1` as a field named
// "foo\t1". Quoted string defaults keep their inner spaces — only the leading
// separator is consumed.
func cutSpace(s string) (head, rest string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// cutConstant reports whether rest declares a constant and, if so, splits it
// into name and value. A `=` is only a constant separator when the text before
// it is a single bare token — that keeps `string foo "a=b"` a field with a
// default rather than a constant.
func cutConstant(rest string) (name, value string, ok bool) {
	i := strings.Index(rest, "=")
	if i < 0 {
		return "", "", false
	}
	name = strings.TrimSpace(rest[:i])
	if name == "" || strings.ContainsAny(name, " \t\"'") {
		return "", "", false
	}
	return name, strings.TrimSpace(rest[i+1:]), true
}

// splitComment splits a line into code and its trailing comment, ignoring `#`
// inside a quoted default value.
func splitComment(line string) (code, comment string) {
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return line[:i], strings.TrimSpace(line[i+1:])
		}
	}
	return line, ""
}
