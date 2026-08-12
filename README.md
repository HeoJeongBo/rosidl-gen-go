# rosidl-gen-go

ROS 2 interface definitions (`.msg` / `.srv`) → plain Go structs.

A Go program talks DDS by CDR-encoding plain structs, and that codec is
positional: struct field order *is* the wire contract. Hand-mirroring a `.msg`
breaks silently the moment a field is inserted upstream — this generator
removes the hand step.

It emits no serialization code. The structs are laid out so that a
reflection-based CDR codec (for example `github.com/lesomnus/cdr`) frames them
correctly, which keeps the generated code readable and the codec replaceable.

## Usage

```sh
go run github.com/HeoJeongBo/rosidl-gen-go -config rosidl-gen.yaml         # generate
go run github.com/HeoJeongBo/rosidl-gen-go -config rosidl-gen.yaml -check  # verify, write nothing
go run github.com/HeoJeongBo/rosidl-gen-go -config rosidl-gen.yaml -v      # list resolved interfaces
```

`-check` re-renders everything, compares it to disk byte for byte, and reports
`missing`, `outdated`, and `stale` files. Commit the generated files and run
`-check` in CI so interface drift fails the build instead of shipping quietly.

The repository ships a self-contained example; run it from a checkout:

```sh
go run . -config example/rosidl-gen.yaml -v
```

## Configuration

The example config, annotated — every path is resolved relative to the config
file itself:

```yaml
out: out                   # output directory
package: demo              # Go package name for every emitted file

search_paths:              # scanned for ament packages; earlier entries win,
  - msg                    # so a local definition beats an installed one.
  - ${ROS_ROOT}/share      # ${VAR} is expanded; an unset var drops the entry

generate:                  # patterns; transitive dependencies come along
  - demo_msgs/**           # automatically, so list only what you talk to

names:                     # optional: mirror a C++ name header into Go
  header: include/names.h
  out: out/names.g.go
```

Two more keys apply once you depend on interfaces you do not want generated:

```yaml
imports:                   # qualifier -> Go import path, used by `external`
  ros: github.com/lesomnus/cdr/ros

external:                  # bind an interface to a Go type that already exists
  std_msgs/msg/Header: ros.Header    # qualified: resolved through `imports`
  std_srvs/srv/Trigger: Trigger      # unqualified: hand-written in `package`
```

A `.msg` maps to the full Go type name; a `.srv` maps to the prefix of its
`<prefix>Request` / `<prefix>Response` pair. The remaining keys are `rename`
(override a derived Go identifier, needed only to break a collision between two
packages) and `emit.as_error` (see below).

Core keys are strict: a typo is an error. An unknown top-level section is kept
for an extension emitter to claim — `names:` is claimed by the built-in
`cppnames` emitter — and anything left unclaimed is an error, so typos in
extension sections fail loudly too. A `generate` pattern matching nothing is
also an error.

## What gets emitted

One `<ros_package>.g.go` per ROS package, containing for each interface:

- `const <Type>Type = "pkg/kind/Type"` — the rcl typename
- the message constants, prefixed with the type name
- the struct: field order is the CDR wire layout, and `.msg` comments carry
  over as Go doc comments
- `New<Type>()` when the definition declares non-zero field defaults
- `AsError() error` on a service response carrying the `bool success` +
  `string message` pair; set `emit.as_error: false` to suppress it

Plus `registry.g.go` (`GeneratedTypes`, every generated type keyed by canonical
name, for reflection-based checks) and, when `names:` is configured,
`names.g.go` — constants and lookup tables scraped from the C++ header along
with a `MirroredNames` reverse map.

Stale `*.g.go` files in the output directory are pruned on generation and
reported by `-check`. Pruning is non-recursive, touches only the `.g.go`
suffix, and never removes a file the run just emitted, so hand-written code in
the same directory is safe.

## Library use

The parser and the generator are both importable:

```go
import (
    "github.com/HeoJeongBo/rosidl-gen-go/gogen"
    "github.com/HeoJeongBo/rosidl-gen-go/rosidl"
)
```

`rosidl` parses `.msg`/`.srv` and indexes ament search paths; `gogen` loads the
config, resolves the interface set, and emits Go source. A downstream contract
test can reflect over the parsed AST (`rosidl.Message`, `rosidl.Type`, …) to
check hand-written mirrors against the definitions they came from.

```go
cfg, err := gogen.LoadConfig("rosidl-gen.yaml")
// ...
out, err := gogen.Run(cfg)   // resolve and emit everything
stats, err := out.Write()    // or out.Check() to gate on drift
```

`Output.Check` returns a structured `*gogen.DriftError` categorizing missing,
outdated, and stale files.

## Extending

The core output — per-package files plus the registry — is fixed. Additional
artifacts come from `gogen.Emitter` implementations, which see the resolved
interface set, the parsed definitions, and the config, and return files placed
relative to the config directory:

```go
type docs struct{}

func (docs) Name() string { return "docs" }

func (d docs) Emit(g *gogen.Generator) ([]gogen.File, error) {
    var b strings.Builder
    for _, n := range g.Selected() {
        ident, _ := g.GoName(n)
        fmt.Fprintf(&b, "- `%s` -> `%s`\n", n, ident)
    }
    return []gogen.File{{Name: "docs/interfaces.md", Body: []byte(b.String())}}, nil
}

// in your main:
out, err := gogen.Run(cfg, docs{})
```

**[`example/emitter/`](example/emitter) is the working version of that sketch** —
one file holding an emitter and the `main` that composes it, with its own config
and committed golden output. CI runs it in `-check` mode, so the extension path
is compiled and exercised rather than merely described. Run it with:

```sh
go run ./example/emitter -config example/emitter/rosidl-gen.yaml
```

The shipped CLI registers only the built-in emitter, so adding your own means
writing a `main` like that one. There is no config-driven loading of third-party
emitters: Go's runtime plugin support is Linux-only and requires an identical
toolchain and build flags, which is a worse contract than a twenty-line program.

An emitter's options live in its own top-level config section, decoded strictly
via `cfg.Section("mysection", &opts)`; anything left unclaimed should be rejected
with `cfg.UnclaimedSections()`, which is what keeps a typo loud.

What an emitter can ask the generator:

| | |
| --- | --- |
| `Selected()` | the resolved interface set, sorted |
| `GoName(n)` | the Go identifier for a message, or a service's shared prefix |
| `MessageIdent(n, m)` | the identifier for one message body — a `.srv` becomes `<prefix>Request` and `<prefix>Response` |
| `GoType(t)` | the Go type a field is emitted as, plus the import qualifiers it needs |
| `Externals()` / `ExternalType(n)` | interfaces bound by `external`, which are deliberately absent from `Selected` |
| `Provenance(n)` | why an interface was selected: a pattern, or the field that referenced it |
| `Index()` | the parsed definitions |
| `Config()` | paths, `imports`, and your own section |

Emitters producing Go source can also reuse `gogen.Pascal`,
`gogen.CommentBlock`, `gogen.FieldNote`, and `gogen.FormatFile`.

The built-in `cppnames` emitter is the other worked example, and a less trivial
one: it scrapes `const char*` scalars and `std::array<std::pair<...>>` tables out
of a C++ header, resolves entries whose value references another symbol, and
refuses to emit when a table parses short or a declaration is wrapped across
lines — the two ways an entry can go missing without anything failing.

## Verification

- `go test ./...` — parser and name-header unit tests
- CI (`.github/workflows/ci.yaml`) — gofmt, vet, tests, and a byte-check of the
  committed golden output under `example/out` and `example/emitter/out`
- `scripts/verify-reference.sh` — check the generator against a project outside
  this repository that already tracks its generated output. Point `REF_CONFIG`
  at that project's config; supply `ROS_SHARE` (or `ROS_SHARE_GIT`) when it
  resolves interfaces through `${ROS_ROOT}/share`. It runs in `-check` mode, so
  nothing outside a temporary directory is written. Not run in CI, which has no
  such project to point at.

Byte stability depends on the toolchain's gofmt, so the module pins `go 1.26.3`
and CI installs exactly that via `go-version-file`.

## Devcontainer

`.devcontainer/` runs the repository in `golang:1.26.3` with Go module and
build cache volumes, and re-checks the example golden output on start.

## License

MIT — see [LICENSE](LICENSE).
