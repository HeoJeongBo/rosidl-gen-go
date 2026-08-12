package gogen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/HeoJeongBo/rosidl-gen-go/rosidl"
)

// File is one generated source file.
type File struct {
	// Name is where the file goes, and what it is relative to depends on who
	// produced it: a core file resolves inside the config's `out` directory,
	// while an [Emitter]'s file resolves against the config file's directory,
	// so an emitter can write outside `out`.
	Name string
	// Body is the file's contents. Core output is gofmt-ed; an emitter's is
	// whatever it returned.
	Body []byte
}

// Generator resolves the configured interface set and emits Go source for it.
type Generator struct {
	cfg *Config
	ix  *rosidl.Index

	external map[rosidl.Name]string
	rename   map[rosidl.Name]string

	selected   []rosidl.Name
	inSet      map[rosidl.Name]bool
	goName     map[rosidl.Name]string
	provenance map[rosidl.Name]Provenance
}

// New builds a Generator over the definitions reachable from cfg's search paths.
func New(cfg *Config) (*Generator, error) {
	ix, err := rosidl.NewIndex(cfg.ResolvedSearchPaths())
	if err != nil {
		return nil, err
	}

	g := &Generator{
		cfg:        cfg,
		ix:         ix,
		external:   map[rosidl.Name]string{},
		rename:     map[rosidl.Name]string{},
		inSet:      map[rosidl.Name]bool{},
		goName:     map[rosidl.Name]string{},
		provenance: map[rosidl.Name]Provenance{},
	}
	// LoadConfig already rejected malformed keys with the config path and the
	// section named; a caller that built a Config by hand gets that context here.
	for _, k := range sortedKeys(cfg.External) {
		n, err := rosidl.ParseName(k, "")
		if err != nil {
			return nil, fmt.Errorf("external %q: %w", k, err)
		}
		g.external[n] = cfg.External[k]
	}
	for _, k := range sortedKeys(cfg.Rename) {
		n, err := rosidl.ParseName(k, "")
		if err != nil {
			return nil, fmt.Errorf("rename %q: %w", k, err)
		}
		g.rename[n] = cfg.Rename[k]
	}
	return g, nil
}

// Index exposes the underlying definition index so tests can reuse it.
func (g *Generator) Index() *rosidl.Index { return g.ix }

// Config returns the configuration this Generator was built from, so an
// Emitter can resolve paths and read its own configuration section.
func (g *Generator) Config() *Config { return g.cfg }

// Resolve expands the `generate` patterns, pulls in every nested dependency and
// assigns a Go identifier to each selected interface. It fails on an unresolved
// reference or on two interfaces claiming the same Go identifier.
func (g *Generator) Resolve() error {
	type pending struct {
		name rosidl.Name
		prov Provenance
	}

	var queue []pending
	for _, pattern := range g.cfg.Generate {
		matched, err := g.ix.Match(pattern)
		if err != nil {
			return err
		}
		for _, n := range matched {
			queue = append(queue, pending{n, Provenance{Pattern: pattern}})
		}
	}

	for len(queue) > 0 {
		p := queue[0]
		n := p.name
		queue = queue[1:]
		if g.inSet[n] {
			continue
		}
		if _, ok := g.external[n]; ok {
			continue
		}
		if !g.ix.Has(n) {
			return fmt.Errorf("%s: no definition found and no `external` mapping", n)
		}
		g.inSet[n] = true
		g.selected = append(g.selected, n)
		// First reason wins, matching the breadth-first order: the shortest
		// chain from a `generate` pattern is the one worth reporting.
		g.provenance[n] = p.prov

		msgs, err := g.ix.Messages(n)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			for _, f := range m.Fields {
				if !f.Type.IsNested() {
					continue
				}
				ref := f.Type.Nested
				if _, ok := g.external[ref]; ok {
					continue
				}
				if !g.ix.Has(ref) {
					return fmt.Errorf("%s field %q: unresolved type %s (add it to `external` or a search path)",
						n, f.Name, ref)
				}
				queue = append(queue, pending{ref, Provenance{Parent: n, Field: f.Name}})
			}
		}
	}

	slices.SortFunc(g.selected, func(a, b rosidl.Name) int { return strings.Compare(a.String(), b.String()) })

	// Every identifier the core emission puts in the output package, so a
	// collision fails here instead of in the consumer's build. gofmt cannot
	// catch it: format.Source parses, it does not type-check.
	owner := map[string]rosidl.Name{}
	claim := func(ident string, n rosidl.Name) error {
		if prev, ok := owner[ident]; ok {
			if prev == n {
				return fmt.Errorf("%s emits the Go identifier %q twice", n, ident)
			}
			return fmt.Errorf("Go identifier %q claimed by both %s and %s; add a `rename` entry", ident, prev, n)
		}
		owner[ident] = n
		return nil
	}
	// registry.g.go declares this unconditionally.
	owner["GeneratedTypes"] = rosidl.Name{}

	for _, n := range g.selected {
		base := n.Type
		if r, ok := g.rename[n]; ok {
			base = r
		}
		g.goName[n] = base

		if err := claim(base+"Type", n); err != nil {
			return err
		}
		msgs, err := g.ix.Messages(n)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			ident := g.messageIdent(n, m)
			if err := claim(ident, n); err != nil {
				return err
			}
			// A constructor is emitted only for a message that declares
			// non-zero defaults, so only claim the name when it will exist.
			if len(defaultedFields(m)) > 0 {
				if err := claim("New"+ident, n); err != nil {
					return err
				}
			}
			for _, c := range m.Consts {
				if err := claim(ident+Pascal(c.Name), n); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// defaultedFields are the fields whose declared default differs from the Go
// zero value, i.e. the ones a constructor has to restate.
func defaultedFields(m rosidl.Message) []rosidl.Field {
	var out []rosidl.Field
	for _, f := range m.Fields {
		if f.Default != "" && !isZeroDefault(f) {
			out = append(out, f)
		}
	}
	return out
}

// Selected returns the resolved interface set, sorted.
func (g *Generator) Selected() []rosidl.Name { return slices.Clone(g.selected) }

// Provenance says why an interface ended up in the resolved set: either a
// `generate` pattern matched it directly, or another interface referenced it
// from a field. Exactly one of Pattern and Parent is set.
type Provenance struct {
	// Pattern is the `generate` entry that matched, when the interface was
	// requested directly.
	Pattern string
	// Parent is the interface whose field pulled this one in.
	Parent rosidl.Name
	// Field is that field's name in the .msg.
	Field string
}

// Provenance reports why n is in the resolved set. ok is false for an
// interface that was not selected.
func (g *Generator) Provenance(n rosidl.Name) (Provenance, bool) {
	p, ok := g.provenance[n]
	return p, ok
}

// Externals returns the interfaces bound to an existing Go type by `external`,
// sorted. They are deliberately absent from Selected: nothing is emitted for
// them, but an emitter documenting the interface set still needs to see them.
func (g *Generator) Externals() []rosidl.Name {
	out := make([]rosidl.Name, 0, len(g.external))
	for n := range g.external {
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b rosidl.Name) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// ExternalType returns the Go type `external` binds n to, e.g. "ros.Header".
func (g *Generator) ExternalType(n rosidl.Name) (string, bool) {
	s, ok := g.external[n]
	return s, ok
}

// MessageIdent is the Go type name emitted for one message body: the type name
// itself for a .msg, or <prefix>Request / <prefix>Response for a .srv half.
// Emitters need it because GoName returns only the shared prefix of a service.
func (g *Generator) MessageIdent(owner rosidl.Name, m rosidl.Message) string {
	return g.messageIdent(owner, m)
}

// GoName returns the Go identifier assigned to a message, or the shared prefix
// of a service's Request/Response pair.
func (g *Generator) GoName(n rosidl.Name) (string, bool) { s, ok := g.goName[n]; return s, ok }

// messageIdent is the Go type name for one message body: the type name itself
// for a .msg, or `<prefix>Request` / `<prefix>Response` for a .srv half.
func (g *Generator) messageIdent(owner rosidl.Name, m rosidl.Message) string {
	base := g.goName[owner]
	switch {
	case strings.HasSuffix(m.Name.Type, "_Request"):
		return base + "Request"
	case strings.HasSuffix(m.Name.Type, "_Response"):
		return base + "Response"
	default:
		return base
	}
}

// Emit renders the core output: one file per ROS package plus the registry.
// Additional artifacts come from [Emitter] implementations via [Generator.Run].
func (g *Generator) Emit() ([]File, error) {
	byPkg := map[string][]rosidl.Name{}
	for _, n := range g.selected {
		byPkg[n.Package] = append(byPkg[n.Package], n)
	}

	var out []File
	for _, pkg := range sortedKeys(byPkg) {
		f, err := g.emitPackage(pkg, byPkg[pkg])
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}

	reg, err := g.emitRegistry()
	if err != nil {
		return nil, err
	}
	return append(out, reg), nil
}

// emitRegistry maps every generated message body to a zero value of its Go type
// so a contract test can reflect over the whole set without a hand-kept table.
func (g *Generator) emitRegistry() (File, error) {
	var b strings.Builder
	b.WriteString("// Code generated by rosidl-gen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", g.cfg.Package)
	b.WriteString("// GeneratedTypes maps each generated interface to a zero value of its Go\n")
	b.WriteString("// type, keyed by canonical name (a service appears as its _Request and\n")
	b.WriteString("// _Response halves). A contract test can reflect over it to prove every struct\n")
	b.WriteString("// still matches the definition it came from.\n")
	b.WriteString("var GeneratedTypes = map[string]any{\n")

	for _, n := range g.selected {
		msgs, err := g.ix.Messages(n)
		if err != nil {
			return File{}, err
		}
		for _, m := range msgs {
			fmt.Fprintf(&b, "\t%q: %s{},\n", m.Name.String(), g.messageIdent(n, m))
		}
	}
	b.WriteString("}\n")

	formatted, err := FormatFile("registry.g.go", []byte(b.String()))
	if err != nil {
		return File{}, err
	}
	return File{Name: "registry.g.go", Body: formatted}, nil
}

func (g *Generator) emitPackage(pkg string, names []rosidl.Name) (File, error) {
	var body strings.Builder
	imports := map[string]bool{}

	for _, n := range names {
		msgs, err := g.ix.Messages(n)
		if err != nil {
			return File{}, err
		}

		// A .srv's leading comment documents the service as a whole. It hangs
		// off the typename constant, which is the service's identity in the
		// generated package; emitted between declarations instead, it attaches
		// to nothing and never reaches `go doc`.
		fmt.Fprintf(&body, "// %sType is the rcl typename of %s.\n", g.goName[n], n)
		if n.Kind == rosidl.KindSrv {
			s, err := g.ix.Service(n)
			if err != nil {
				return File{}, err
			}
			if len(s.Doc) > 0 {
				body.WriteString("//\n")
				body.WriteString(CommentBlock("", s.Doc))
			}
		}
		fmt.Fprintf(&body, "const %sType = %q\n\n", g.goName[n], n.String())

		for _, m := range msgs {
			if err := g.emitMessage(&body, imports, n, m); err != nil {
				return File{}, err
			}
		}
	}

	var head strings.Builder
	head.WriteString("// Code generated by rosidl-gen. DO NOT EDIT.\n")
	fmt.Fprintf(&head, "// source: %s interface definitions\n\n", pkg)
	fmt.Fprintf(&head, "package %s\n\n", g.cfg.Package)
	if len(imports) > 0 {
		head.WriteString("import (\n")
		for _, q := range sortedKeys(imports) {
			path := g.cfg.Imports[q]
			if path == "" {
				path = q // standard library, e.g. fmt
			}
			fmt.Fprintf(&head, "\t%q\n", path)
		}
		head.WriteString(")\n\n")
	}

	formatted, err := FormatFile(pkg+".g.go", []byte(head.String()+body.String()))
	if err != nil {
		return File{}, err
	}
	return File{Name: pkg + ".g.go", Body: formatted}, nil
}

func (g *Generator) emitMessage(b *strings.Builder, imports map[string]bool, owner rosidl.Name, m rosidl.Message) error {
	ident := g.messageIdent(owner, m)

	if len(m.Consts) > 0 {
		fmt.Fprintf(b, "// %s constants, from %s.\n", ident, m.Name)
		b.WriteString("const (\n")
		for _, c := range m.Consts {
			b.WriteString(CommentBlock("\t", c.Doc))
			v, err := constValue(c)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", m.Name, c.Name, err)
			}
			fmt.Fprintf(b, "\t%s%s %s = %s\n", ident, Pascal(c.Name), rosidl.Primitives[c.Type.Primitive], v)
		}
		b.WriteString(")\n\n")
	}

	fmt.Fprintf(b, "// %s is %s.\n", ident, m.Name)
	if len(m.Doc) > 0 {
		b.WriteString("//\n")
		b.WriteString(CommentBlock("", m.Doc))
	}
	b.WriteString("//\n// Field order is the CDR wire layout.\n")
	fmt.Fprintf(b, "type %s struct {\n", ident)

	for _, f := range m.Fields {
		b.WriteString(CommentBlock("\t", f.Doc))
		goType, err := g.goType(f.Type, imports)
		if err != nil {
			return fmt.Errorf("%s field %q: %w", m.Name, f.Name, err)
		}
		fmt.Fprintf(b, "\t%s %s%s\n", Pascal(f.Name), goType, fieldNote(f))
	}
	b.WriteString("}\n\n")

	// Same predicate Resolve claims `New<ident>` with, so the two cannot drift.
	defaults := defaultedFields(m)
	if len(defaults) > 0 {
		if err := g.emitDefaults(b, ident, m, defaults); err != nil {
			return err
		}
	}
	if owner.Kind == rosidl.KindSrv && strings.HasSuffix(ident, "Response") && g.cfg.Emit.asError() && hasResultPair(m) {
		imports["fmt"] = true
		fmt.Fprintf(b, "// AsError reports the failure the response carries, or nil when it succeeded.\n")
		fmt.Fprintf(b, "func (r %s) AsError() error {\n", ident)
		b.WriteString("\tif r.Success {\n\t\treturn nil\n\t}\n")
		b.WriteString("\treturn fmt.Errorf(\"request was not successful: %s\", r.Message)\n}\n\n")
	}
	return nil
}

// hasResultPair reports whether a service response carries the
// success/message result idiom (std_srvs/Trigger), which callers typically
// turn into an error.
func hasResultPair(m rosidl.Message) bool {
	var success, message bool
	for _, f := range m.Fields {
		if f.Type.Array != rosidl.ArrayNone {
			continue
		}
		switch {
		case f.Name == "success" && f.Type.Primitive == "bool":
			success = true
		case f.Name == "message" && f.Type.Primitive == "string":
			message = true
		}
	}
	return success && message
}

// emitDefaults renders a constructor for messages whose .msg declares field
// defaults. The zero value is not the wire default for those, and nothing else
// in the Go tree carries that information.
func (g *Generator) emitDefaults(b *strings.Builder, ident string, m rosidl.Message, defaults []rosidl.Field) error {
	fmt.Fprintf(b, "// New%s returns %s with the field defaults declared in %s.\n", ident, ident, m.Name)
	fmt.Fprintf(b, "// The zero value is NOT the declared default for these fields.\n")
	fmt.Fprintf(b, "func New%s() %s {\n\treturn %s{\n", ident, ident, ident)
	for _, f := range defaults {
		v, err := defaultValue(f)
		if err != nil {
			return fmt.Errorf("%s field %q: %w", m.Name, f.Name, err)
		}
		fmt.Fprintf(b, "\t\t%s: %s,\n", Pascal(f.Name), v)
	}
	b.WriteString("\t}\n}\n\n")
	return nil
}

// GoType renders the Go type a field is emitted as — "[3]float32",
// "[]Reading", "ros.Header" — together with the import qualifiers it needs.
// Resolve must have run.
//
// A qualifier maps to an import path through the config's `imports`; a
// qualifier with no entry there is a standard-library path, e.g. "fmt".
func (g *Generator) GoType(t rosidl.Type) (goType string, qualifiers []string, err error) {
	imports := map[string]bool{}
	s, err := g.goType(t, imports)
	if err != nil {
		return "", nil, err
	}
	return s, sortedKeys(imports), nil
}

// FieldNote is the trailing comment the generator puts on a field for the .msg
// information a Go type cannot express: sequence and string upper bounds, and
// declared defaults. It is empty when there is nothing to say.
func FieldNote(f rosidl.Field) string { return fieldNote(f) }

// fieldNote annotates the Go field with .msg information the Go type cannot
// express: sequence upper bounds, string bounds and declared defaults.
func fieldNote(f rosidl.Field) string {
	var notes []string
	if f.Type.Array == rosidl.ArrayBounded {
		notes = append(notes, fmt.Sprintf("<=%d entries", f.Type.ArraySize))
	}
	if f.Type.StringBound > 0 {
		notes = append(notes, fmt.Sprintf("<=%d bytes", f.Type.StringBound))
	}
	if f.Default != "" {
		notes = append(notes, "default "+f.Default)
	}
	if len(notes) == 0 {
		return ""
	}
	return " // " + strings.Join(notes, ", ")
}

func (g *Generator) goType(t rosidl.Type, imports map[string]bool) (string, error) {
	base, err := g.goBase(t, imports)
	if err != nil {
		return "", err
	}
	switch t.Array {
	case rosidl.ArrayNone:
		return base, nil
	case rosidl.ArrayFixed:
		return fmt.Sprintf("[%d]%s", t.ArraySize, base), nil
	default:
		return "[]" + base, nil
	}
}

func (g *Generator) goBase(t rosidl.Type, imports map[string]bool) (string, error) {
	if !t.IsNested() {
		return rosidl.Primitives[t.Primitive], nil
	}
	if ext, ok := g.external[t.Nested]; ok {
		if q, _, hasQual := strings.Cut(ext, "."); hasQual {
			imports[q] = true
		}
		return ext, nil
	}
	if name, ok := g.goName[t.Nested]; ok {
		return name, nil
	}
	return "", fmt.Errorf("unresolved type %s", t.Nested)
}

func constValue(c rosidl.Const) (string, error) {
	switch c.Type.Primitive {
	case "string":
		v := strings.TrimSpace(c.Value)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		return strconv.Quote(v), nil
	case "bool":
		return normalizeBool(c.Value)
	default:
		return strings.TrimSpace(c.Value), nil
	}
}

func defaultValue(f rosidl.Field) (string, error) {
	if f.Type.Array != rosidl.ArrayNone {
		return "", fmt.Errorf("array default %q is not supported", f.Default)
	}
	return constValue(rosidl.Const{Type: f.Type, Value: f.Default})
}

// isZeroDefault reports whether the declared default is already the Go zero
// value, in which case restating it in a constructor only adds noise.
func isZeroDefault(f rosidl.Field) bool {
	v, err := defaultValue(f)
	if err != nil {
		return false
	}
	switch f.Type.Primitive {
	case "bool":
		return v == "false"
	case "string":
		return v == `""`
	default:
		n, err := strconv.ParseFloat(v, 64)
		return err == nil && n == 0
	}
}

func normalizeBool(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1":
		return "true", nil
	case "false", "0":
		return "false", nil
	default:
		return "", fmt.Errorf("bad bool literal %q", v)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
