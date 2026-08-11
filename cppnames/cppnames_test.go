package cppnames

import (
	"strings"
	"testing"
)

// A table entry may name another symbol instead of repeating the literal.
// Reading only the literal form drops that entry from the generated map, and a
// dropped entry is silent: the name simply disappears, so the caller binds
// nothing for it and the endpoint goes missing at runtime rather than failing
// the build.
func TestParseNameHeaderResolvesSymbolValues(t *testing.T) {
	const src = `
inline constexpr const char* kResetAllService = "demo/reset/all";

inline constexpr std::array<std::pair<const char*, const char*>, 2> kResetTargets = {{
    {"default", kResetAllService},
    {"soft", "demo/reset/soft"},
}};
`
	_, tables, err := parseNameHeader(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(tables))
	}
	got := tables[0].ByKey
	if got["default"] != "demo/reset/all" {
		t.Errorf(`ByKey["default"] = %q, want the referenced scalar's value`, got["default"])
	}
	if got["soft"] != "demo/reset/soft" {
		t.Errorf(`ByKey["soft"] = %q`, got["soft"])
	}
}

// A section rule is decoration. A labelled one contains letters, so a bare
// all-dashes test would let it through and attach it as the doc comment of the
// declaration below — silently, since the result still compiles.
func TestParseNameHeaderDropsSectionRules(t *testing.T) {
	for _, tc := range []struct{ name, rule string }{
		{"ascii labelled", "// --- Topics ---------------"},
		{"ascii bare", "// -------------------------"},
		{"box drawing labelled", "// ── Topics ──────"},
		{"box drawing bare", "// ──────────"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.rule + "\ninline constexpr const char* kStateTopic = \"demo/state\";\n"
			scalars, _, err := parseNameHeader(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(scalars) != 1 {
				t.Fatalf("scalars = %d, want 1", len(scalars))
			}
			if len(scalars[0].Doc) != 0 {
				t.Errorf("section rule captured as documentation: %q", scalars[0].Doc)
			}
		})
	}
}

// A real comment directly above a declaration is documentation and must survive.
func TestParseNameHeaderKeepsDocComment(t *testing.T) {
	const src = `
// Periodic device state.
inline constexpr const char* kStateTopic = "demo/state";
`
	scalars, _, err := parseNameHeader(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(scalars) != 1 || len(scalars[0].Doc) != 1 || scalars[0].Doc[0] != "Periodic device state." {
		t.Fatalf("Doc = %q, want the comment line", scalars[0].Doc)
	}
}

// The scalar may be declared after the table that references it.
func TestParseNameHeaderResolvesForwardReference(t *testing.T) {
	const src = `
inline constexpr std::array<std::pair<const char*, const char*>, 1> kFoo = {{
    {"a", kLater},
}};

inline constexpr const char* kLater = "demo/later";
`
	_, tables, err := parseNameHeader(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v := tables[0].ByKey["a"]; v != "demo/later" {
		t.Errorf(`ByKey["a"] = %q, want "demo/later"`, v)
	}
}

func TestParseNameHeaderRejectsUndeclaredSymbol(t *testing.T) {
	const src = `
inline constexpr std::array<std::pair<const char*, const char*>, 1> kFoo = {{
    {"a", kMissing},
}};
`
	if _, _, err := parseNameHeader(src); err == nil {
		t.Fatal("want an error naming the undeclared symbol")
	} else if !strings.Contains(err.Error(), "kMissing") {
		t.Errorf("error %q does not name the symbol", err)
	}
}

// The declared element count is the backstop for a spelling this parser cannot
// read at all: without it the table just comes out short.
func TestParseNameHeaderRejectsShortTable(t *testing.T) {
	const src = `
inline constexpr std::array<std::pair<const char*, const char*>, 3> kFoo = {{
    {"a", "demo/a"},
    {"b", "demo/b"},
}};
`
	_, _, err := parseNameHeader(src)
	if err == nil {
		t.Fatal("want an error for a table that parsed short")
	}
	if !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "2") {
		t.Errorf("error %q does not report declared vs parsed counts", err)
	}
}

// A declared size the parser cannot read (a named constant) would silently
// disable the short-parse guard; it must be an error instead.
func TestParseNameHeaderRejectsNonLiteralSize(t *testing.T) {
	const src = `
inline constexpr std::array<std::pair<const char*, const char*>, kNumParts> kFoo = {{
    {"a", "demo/a"},
}};
`
	if _, _, err := parseNameHeader(src); err == nil {
		t.Fatal("want an error for a non-literal std::array size")
	}
}

// The generated file header is assembled from config: `source:` overrides the
// `// source:` spelling (defaulting to `header:` as written) and `comment:`
// adds a doc block line for line, so a consumer can reproduce an existing
// hand-written header byte for byte.
func TestRenderHeaderFromConfig(t *testing.T) {
	const src = `
inline constexpr const char* kStateTopic = "demo/state";
`
	cases := []struct {
		name string
		c    Config
		want string
	}{
		{
			name: "default source, no comment",
			c:    Config{Header: "include/names.h", Out: "out/names.g.go"},
			want: "// Code generated by rosidl-gen. DO NOT EDIT.\n" +
				"// source: include/names.h\n" +
				"\n" +
				"package demo\n",
		},
		{
			name: "source override and comment block",
			c: Config{
				Header:  "msg/names.h",
				Out:     "out/names.g.go",
				Source:  "interfaces/include/names.h",
				Comment: "Rename in the header, not here;\nevery consumer shares it.\n",
			},
			want: "// Code generated by rosidl-gen. DO NOT EDIT.\n" +
				"// source: interfaces/include/names.h\n" +
				"//\n" +
				"// Rename in the header, not here;\n" +
				"// every consumer shares it.\n" +
				"\n" +
				"package demo\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := render("demo", src, tc.c)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.HasPrefix(string(got), tc.want) {
				t.Errorf("render header:\n%s\nwant prefix:\n%s", got, tc.want)
			}
		})
	}
}

// A formatter that wraps the std::array head across lines makes the whole
// table invisible to the parser — not short, absent — so the declaration
// count is the only thing that can notice.
func TestParseNameHeaderRejectsWrappedDeclaration(t *testing.T) {
	const src = `
inline constexpr std::array<std::pair<const char*, const char*>, 1>
    kFoo = {{
        {"a", "demo/a"},
    }};
`
	if _, _, err := parseNameHeader(src); err == nil {
		t.Fatal("want an error for a wrapped std::array declaration")
	} else if !strings.Contains(err.Error(), "wrapped") {
		t.Errorf("error %q does not mention wrapping", err)
	}
}
