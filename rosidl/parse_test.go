package rosidl

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func msgName(t string) Name { return Name{Package: "pkg", Kind: KindMsg, Type: t} }

func TestParseMessageFields(t *testing.T) {
	m, err := ParseMessage(msgName("X"), "X.msg", `
bool flag
uint8[16] mask
string<=32 label_id
pkg/Other[<=2] others
other_pkg/Thing thing
other_pkg/msg/Explicit explicit
Sibling sibling
float32[] samples
`)
	require.NoError(t, err)

	want := []struct {
		name string
		typ  string
	}{
		{"flag", "bool"},
		{"mask", "uint8[16]"},
		{"label_id", "string<=32"},
		{"others", "pkg/Other[<=2]"},
		{"thing", "other_pkg/Thing"},
		{"explicit", "other_pkg/Explicit"},
		{"sibling", "pkg/Sibling"},
		{"samples", "float32[]"},
	}
	require.Len(t, m.Fields, len(want))
	for i, w := range want {
		require.Equal(t, w.name, m.Fields[i].Name, "field %d name", i)
		require.Equal(t, w.typ, m.Fields[i].Type.String(), "field %d type", i)
	}

	require.Equal(t, ArrayFixed, m.Fields[1].Type.Array)
	require.Equal(t, 16, m.Fields[1].Type.ArraySize)
	require.Equal(t, 32, m.Fields[2].Type.StringBound)
	require.Equal(t, ArrayBounded, m.Fields[3].Type.Array)
	require.Equal(t, ArrayDynamic, m.Fields[7].Type.Array)
	// A bare nested reference resolves against the enclosing package.
	require.Equal(t, "pkg", m.Fields[6].Type.Nested.Package)
}

func TestParseMessageConstantsAndDefaults(t *testing.T) {
	m, err := ParseMessage(msgName("X"), "X.msg", `
uint8 STATUS_OFF=0
uint8 STATUS_RUN = 3
string LABEL="hi"
uint8 mode 3
bool tracking false
float32 weight 5.0
`)
	require.NoError(t, err)

	require.Len(t, m.Consts, 3)
	require.Equal(t, "STATUS_OFF", m.Consts[0].Name)
	require.Equal(t, "0", m.Consts[0].Value)
	require.Equal(t, "3", m.Consts[1].Value)
	require.Equal(t, `"hi"`, m.Consts[2].Value)

	require.Len(t, m.Fields, 3)
	require.Equal(t, "3", m.Fields[0].Default)
	require.Equal(t, "false", m.Fields[1].Default)
	require.Equal(t, "5.0", m.Fields[2].Default)
}

func TestParseMessageComments(t *testing.T) {
	m, err := ParseMessage(msgName("X"), "X.msg", `# Leading block documents
# the message, even with no blank line before the first field.
bool first

# attached to second
bool second  # trailing too

# orphaned by the blank line below

bool third
`)
	require.NoError(t, err)

	require.Equal(t, []string{
		"Leading block documents",
		"the message, even with no blank line before the first field.",
	}, m.Doc)
	require.Empty(t, m.Fields[0].Doc)
	require.Equal(t, []string{"attached to second", "trailing too"}, m.Fields[1].Doc)
	require.Empty(t, m.Fields[2].Doc)
}

func TestParseMessageHashInsideDefault(t *testing.T) {
	m, err := ParseMessage(msgName("X"), "X.msg", `string label "a#b" # real comment`)
	require.NoError(t, err)
	require.Len(t, m.Fields, 1)
	require.Equal(t, `"a#b"`, m.Fields[0].Default)
	require.Equal(t, []string{"real comment"}, m.Fields[0].Doc)
}

func TestParseMessageEqualsInsideDefaultIsNotAConstant(t *testing.T) {
	m, err := ParseMessage(msgName("X"), "X.msg", `string expr "a=b"`)
	require.NoError(t, err)
	require.Empty(t, m.Consts)
	require.Len(t, m.Fields, 1)
	require.Equal(t, "expr", m.Fields[0].Name)
}

// A constants-only .msg is field-less, and rosidl gives it the dummy member in
// the generated C struct — so the wire form is one byte, not zero. Losing it
// would shorten every payload of that type with nothing to catch it: a
// contract test builds its expectation from this same parser.
func TestParseMessageFieldLessGetsDummyMember(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"constants only", "uint8 KIND_A=0\nuint8 KIND_B=1\n"},
		{"empty", "# nothing but a comment\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseMessage(msgName("X"), "X.msg", tc.body)
			require.NoError(t, err)
			require.Len(t, m.Fields, 1)
			require.Equal(t, EmptyMemberField, m.Fields[0].Name)
			require.Equal(t, "uint8", m.Fields[0].Type.Primitive)
		})
	}
}

func TestParseServiceEmptyHalvesGetDummyMember(t *testing.T) {
	n := Name{Package: "pkg", Kind: KindSrv, Type: "Get"}
	s, err := ParseService(n, "Get.srv", "# Request\n---\n# Response\nstring value\n")
	require.NoError(t, err)

	require.Equal(t, "pkg/srv/Get_Request", s.Request.Name.String())
	require.Equal(t, "pkg/srv/Get_Response", s.Response.Name.String())

	require.Len(t, s.Request.Fields, 1)
	require.Equal(t, EmptyMemberField, s.Request.Fields[0].Name)
	require.Equal(t, "uint8", s.Request.Fields[0].Type.Primitive)

	require.Len(t, s.Response.Fields, 1)
	require.Equal(t, "value", s.Response.Fields[0].Name)

	// `# Request` / `# Response` are separators, not documentation.
	require.Empty(t, s.Doc)
	require.Empty(t, s.Response.Doc)
}

func TestParseServiceHoistsLeadingDoc(t *testing.T) {
	n := Name{Package: "pkg", Kind: KindSrv, Type: "Plan"}
	s, err := ParseService(n, "Plan.srv", "# What this service does.\nstring goal\n---\nbool success\n")
	require.NoError(t, err)
	require.Equal(t, []string{"What this service does."}, s.Doc)
	require.Empty(t, s.Request.Doc)
}

func TestParseServiceRequiresSeparator(t *testing.T) {
	n := Name{Package: "pkg", Kind: KindSrv, Type: "Bad"}
	_, err := ParseService(n, "Bad.srv", "string only\n")
	require.ErrorContains(t, err, "separator")
}

func TestParseMessageRejectsUnknownShapes(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no name", "bool\n", "has no name"},
		{"bad bound", "uint8[<=x] a\n", "bad array bound"},
		{"bound on non-string", "uint8<=4 a\n", "non-string"},
		{"array constant", "uint8[2] A=1\n", "array constant"},
		// The codec has no UTF-16 path; emitting a Go `string` would compile and
		// pass a reflection-based layout check while mis-framing the wire.
		{"wstring", "wstring name\n", "unsupported type"},
		{"bounded wstring", "wstring<=8 name\n", "unsupported type"},
		{"wstring sequence", "wstring[] names\n", "unsupported type"},
		{"wchar", "wchar c\n", "unsupported type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMessage(msgName("X"), "X.msg", tc.body)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestParseName(t *testing.T) {
	n, err := ParseName("demo_msgs/msg/BatteryState", "")
	require.NoError(t, err)
	require.Equal(t, Name{Package: "demo_msgs", Kind: KindMsg, Type: "BatteryState"}, n)

	n, err = ParseName("std_srvs/srv/Trigger", "")
	require.NoError(t, err)
	require.Equal(t, KindSrv, n.Kind)

	_, err = ParseName("a/b/c/d", "")
	require.ErrorContains(t, err, "malformed")

	_, err = ParseName("Bare", "")
	require.ErrorContains(t, err, "unqualified")
}

// Tabs are legal separators in a .msg. Cutting on the space character alone
// misread `uint8\tfoo` as nameless and put a tab-separated default into the
// field name, which then failed gofmt with an opaque error.
func TestParseMessageTabSeparators(t *testing.T) {
	m, err := ParseMessage(Name{Package: "pkg", Kind: KindMsg, Type: "Foo"}, "Foo.msg",
		"uint8\tstate\nstring\tname\t\"a b\"\nuint8\tMODE_A=1\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Fields) != 2 || m.Fields[0].Name != "state" || m.Fields[1].Name != "name" {
		t.Fatalf("fields = %+v", m.Fields)
	}
	if m.Fields[1].Default != "\"a b\"" {
		t.Fatalf("quoted default with an inner space lost: %q", m.Fields[1].Default)
	}
	if len(m.Consts) != 1 || m.Consts[0].Name != "MODE_A" || m.Consts[0].Value != "1" {
		t.Fatalf("consts = %+v", m.Consts)
	}
}
