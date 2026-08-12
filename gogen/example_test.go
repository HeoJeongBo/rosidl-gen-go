package gogen_test

import (
	"fmt"
	"log"
	"strings"

	"github.com/HeoJeongBo/rosidl-gen-go/cppnames"
	"github.com/HeoJeongBo/rosidl-gen-go/gogen"
)

// Generate everything the config asks for. Check() instead of Write() is the
// CI form: it compares byte for byte and writes nothing.
//
// Note the cppnames registration. A config section belongs to whichever
// emitter claims it, so a caller that skips one gets output missing that
// emitter's files — and Check reports them as stale, which is the right answer
// to the wrong question.
func ExampleRun() {
	cfg, err := gogen.LoadConfig("../example/rosidl-gen.yaml")
	if err != nil {
		log.Fatal(err)
	}

	var extra []gogen.Emitter
	if names, ok, err := cppnames.FromConfig(cfg); err != nil {
		log.Fatal(err)
	} else if ok {
		extra = append(extra, names)
	}

	out, err := gogen.Run(cfg, extra...)
	if err != nil {
		log.Fatal(err)
	}
	if err := out.Check(); err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(out.Paths()), "files")
	// Output: 3 files
}

// docs is a minimal [gogen.Emitter]: it derives one more artifact from the
// same resolved interface set. See example/emitter for a runnable program.
type docs struct{}

func (docs) Name() string { return "docs" }

func (docs) Emit(g *gogen.Generator) ([]gogen.File, error) {
	var b strings.Builder
	for _, n := range g.Selected() {
		ident, _ := g.GoName(n)
		fmt.Fprintf(&b, "- `%s` -> `%s`\n", n, ident)
	}
	return []gogen.File{{Name: "docs/interfaces.md", Body: []byte(b.String())}}, nil
}

func ExampleEmitter() {
	cfg, err := gogen.LoadConfig("../example/rosidl-gen.yaml")
	if err != nil {
		log.Fatal(err)
	}
	names, _, err := cppnames.FromConfig(cfg)
	if err != nil {
		log.Fatal(err)
	}

	out, err := gogen.Run(cfg, names, docs{})
	if err != nil {
		log.Fatal(err)
	}
	// Two core files, the name mirror, and the emitter's own.
	fmt.Println(len(out.Paths()), "files")
	// Output: 4 files
}

// A service is two message bodies, and the generator names them. Asking it
// beats re-deriving the convention.
func ExampleGenerator_MessageIdent() {
	cfg, err := gogen.LoadConfig("../example/rosidl-gen.yaml")
	if err != nil {
		log.Fatal(err)
	}
	g, err := gogen.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := g.Resolve(); err != nil {
		log.Fatal(err)
	}

	for _, n := range g.Selected() {
		msgs, err := g.Index().Messages(n)
		if err != nil {
			log.Fatal(err)
		}
		if len(msgs) == 2 { // a .srv
			fmt.Println(g.MessageIdent(n, msgs[0]), g.MessageIdent(n, msgs[1]))
		}
	}
	// Output: SetModeRequest SetModeResponse
}
