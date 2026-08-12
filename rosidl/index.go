package rosidl

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// skipDirs never hold interface definitions worth indexing: colcon leaves
// build/install/log inside the trees it builds, where a generated copy would
// shadow the source it came from, and the rest are large and irrelevant.
var skipDirs = map[string]bool{
	"build": true, "install": true, "log": true, ".git": true, "node_modules": true,
}

// Index maps canonical interface names to their definition files, discovered by
// walking ament packages under a list of search paths. Earlier entries take
// precedence, so a locally checked-in definition wins over an installed one.
type Index struct {
	files map[Name]string
	msgs  map[Name]Message
	srvs  map[Name]Service
	// roots is kept so a pattern that matched nothing can say where the
	// generator actually looked. Without it the error blames the pattern for
	// what is nearly always a search-path problem.
	roots []string
}

// NewIndex discovers every .msg and .srv reachable from searchPaths. A search
// path may be an ament package itself (a directory holding package.xml) or a
// directory of such packages, e.g. `$ROS_ROOT/share`.
func NewIndex(searchPaths []string) (*Index, error) {
	ix := &Index{
		files: map[Name]string{},
		msgs:  map[Name]Message{},
		srvs:  map[Name]Service{},
		roots: slices.Clone(searchPaths),
	}
	for _, root := range searchPaths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("search path %q: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("search path %q is not a directory", root)
		}

		if pkg, ok, err := packageName(root); err != nil {
			return nil, err
		} else if ok {
			if err := ix.addPackage(pkg, root); err != nil {
				return nil, err
			}
			continue
		}

		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() || skipDirs[e.Name()] {
				continue
			}
			dir := filepath.Join(root, e.Name())
			pkg, ok, err := packageName(dir)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if err := ix.addPackage(pkg, dir); err != nil {
				return nil, err
			}
		}
	}
	return ix, nil
}

// packageName reads <name> from dir/package.xml. ok is false when dir is not an
// ament package. The directory name is NOT a substitute: a checkout named
// `my-interfaces` routinely declares the package `my_interfaces`.
func packageName(dir string) (name string, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.xml"))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	var manifest struct {
		Name string `xml:"name"`
	}
	if err := xml.Unmarshal(b, &manifest); err != nil {
		return "", false, fmt.Errorf("%s/package.xml: %w", dir, err)
	}
	if manifest.Name == "" {
		return "", false, fmt.Errorf("%s/package.xml: empty <name>", dir)
	}
	return manifest.Name, true, nil
}

func (ix *Index) addPackage(pkg, dir string) error {
	for _, kind := range []Kind{KindMsg, KindSrv} {
		entries, err := os.ReadDir(filepath.Join(dir, string(kind)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			ext := "." + string(kind)
			if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
				continue
			}
			n := Name{Package: pkg, Kind: kind, Type: strings.TrimSuffix(e.Name(), ext)}
			if _, dup := ix.files[n]; dup {
				continue // earlier search path wins
			}
			ix.files[n] = filepath.Join(dir, string(kind), e.Name())
		}
	}
	return nil
}

// Has reports whether the index knows a definition for n.
func (ix *Index) Has(n Name) bool { _, ok := ix.files[n]; return ok }

// Path returns the source file backing n.
func (ix *Index) Path(n Name) (string, bool) { p, ok := ix.files[n]; return p, ok }

// Names returns every indexed name, sorted, for deterministic iteration.
func (ix *Index) Names() []Name {
	out := make([]Name, 0, len(ix.files))
	for n := range ix.files {
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b Name) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// Message parses and caches the message named n.
func (ix *Index) Message(n Name) (Message, error) {
	if m, ok := ix.msgs[n]; ok {
		return m, nil
	}
	path, ok := ix.files[n]
	if !ok {
		return Message{}, fmt.Errorf("no definition found for %s", n)
	}
	if n.Kind == KindSrv {
		// A `<Type>_Request` / `<Type>_Response` name resolves through its service.
		return Message{}, fmt.Errorf("%s is a service; use Service()", n)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Message{}, err
	}
	m, err := ParseMessage(n, path, string(b))
	if err != nil {
		return Message{}, err
	}
	ix.msgs[n] = m
	return m, nil
}

// Service parses and caches the service named n.
func (ix *Index) Service(n Name) (Service, error) {
	if s, ok := ix.srvs[n]; ok {
		return s, nil
	}
	path, ok := ix.files[n]
	if !ok {
		return Service{}, fmt.Errorf("no definition found for %s", n)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Service{}, err
	}
	s, err := ParseService(n, path, string(b))
	if err != nil {
		return Service{}, err
	}
	ix.srvs[n] = s
	return s, nil
}

// Lookup resolves any message body by name, including a service's implicit
// `<Type>_Request` / `<Type>_Response`, which have no file of their own.
func (ix *Index) Lookup(n Name) (Message, error) {
	if n.Kind != KindSrv {
		return ix.Message(n)
	}

	base, isReq := strings.CutSuffix(n.Type, "_Request")
	if !isReq {
		var isRes bool
		if base, isRes = strings.CutSuffix(n.Type, "_Response"); !isRes {
			return Message{}, fmt.Errorf("%s is a service; ask for its _Request or _Response", n)
		}
	}

	s, err := ix.Service(Name{Package: n.Package, Kind: KindSrv, Type: base})
	if err != nil {
		return Message{}, err
	}
	if isReq {
		return s.Request, nil
	}
	return s.Response, nil
}

// Messages returns the message bodies of n: the message itself for KindMsg, or
// the request and response pair for KindSrv.
func (ix *Index) Messages(n Name) ([]Message, error) {
	switch n.Kind {
	case KindMsg:
		m, err := ix.Message(n)
		if err != nil {
			return nil, err
		}
		return []Message{m}, nil
	case KindSrv:
		s, err := ix.Service(n)
		if err != nil {
			return nil, err
		}
		return []Message{s.Request, s.Response}, nil
	default:
		return nil, fmt.Errorf("unknown kind %q", n.Kind)
	}
}

// Match expands a `generate` pattern into concrete names. Supported forms are
// `pkg/**`, `pkg/msg/*`, `pkg/srv/*` and a fully qualified `pkg/kind/Type`.
func (ix *Index) Match(pattern string) ([]Name, error) {
	var out []Name
	for _, n := range ix.Names() {
		ok, err := matchName(pattern, n)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, ix.noMatchError(pattern)
	}
	return out, nil
}

// Packages returns the ament package names the index found, sorted.
func (ix *Index) Packages() []string {
	seen := map[string]bool{}
	for n := range ix.files {
		seen[n.Package] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// noMatchError explains a pattern that selected nothing. An empty index is a
// different failure from a wrong pattern — it means nothing was scanned — and
// saying so is the difference between a one-line fix and an afternoon.
func (ix *Index) noMatchError(pattern string) error {
	if len(ix.files) == 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "pattern %q matched no interface: no ament packages were found", pattern)
		if len(ix.roots) == 0 {
			b.WriteString("\n\tno search path was scanned; every `search_paths` entry was skipped")
			b.WriteString("\n\trun `rosidl-gen list` to see why each one was dropped")
			return errors.New(b.String())
		}
		b.WriteString("\n\tscanned:")
		for _, r := range ix.roots {
			fmt.Fprintf(&b, "\n\t  %s", r)
		}
		b.WriteString("\n\ta search path must be an ament package (a directory holding package.xml)")
		b.WriteString("\n\tor a directory of them; the scan does not descend further than that")
		return errors.New(b.String())
	}

	pkgs := ix.Packages()
	var b strings.Builder
	fmt.Fprintf(&b, "pattern %q matched no interface\n\tpackages found: %s",
		pattern, strings.Join(pkgs, ", "))
	b.WriteString("\n\trun `rosidl-gen list` for the full interface list")
	return errors.New(b.String())
}

func matchName(pattern string, n Name) (bool, error) {
	parts := strings.Split(pattern, "/")
	switch len(parts) {
	case 2:
		if parts[1] != "**" {
			return false, fmt.Errorf("malformed pattern %q; want `pkg/**`", pattern)
		}
		return parts[0] == n.Package, nil
	case 3:
		if parts[0] != n.Package || parts[1] != string(n.Kind) {
			return false, nil
		}
		return parts[2] == "*" || parts[2] == n.Type, nil
	default:
		return false, fmt.Errorf("malformed pattern %q", pattern)
	}
}
