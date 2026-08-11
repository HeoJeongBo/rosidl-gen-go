// Package rosidl parses ROS2 interface definitions (.msg / .srv) into an AST
// and indexes them by their canonical `<package>/<kind>/<Type>` name.
//
// It is deliberately independent of any ROS installation: definitions are read
// as text from search paths, so the generator and its tests run on a bare
// checkout.
package rosidl

import (
	"fmt"
	"strings"
)

// Kind is the interface kind of a definition.
type Kind string

const (
	// KindMsg is a message definition (.msg).
	KindMsg Kind = "msg"
	// KindSrv is a service definition (.srv).
	KindSrv Kind = "srv"
)

// Name identifies an interface as `<package>/<kind>/<Type>`, the same form an
// rcl typesupport loader takes as its typename.
type Name struct {
	Package string
	Kind    Kind
	Type    string
}

func (n Name) String() string {
	return fmt.Sprintf("%s/%s/%s", n.Package, n.Kind, n.Type)
}

// IsZero reports whether n is unset.
func (n Name) IsZero() bool { return n.Package == "" && n.Type == "" }

// ParseName parses a canonical `pkg/kind/Type` or the abbreviated `pkg/Type`
// form used inside .msg field declarations (where the kind is always msg).
// A bare `Type` resolves against fallbackPkg, matching rosidl's same-package
// reference rule.
func ParseName(s, fallbackPkg string) (Name, error) {
	parts := strings.Split(s, "/")
	switch len(parts) {
	case 1:
		if fallbackPkg == "" {
			return Name{}, fmt.Errorf("unqualified type %q with no enclosing package", s)
		}
		return Name{Package: fallbackPkg, Kind: KindMsg, Type: parts[0]}, nil
	case 2:
		return Name{Package: parts[0], Kind: KindMsg, Type: parts[1]}, nil
	case 3:
		k := Kind(parts[1])
		if k != KindMsg && k != KindSrv {
			return Name{}, fmt.Errorf("unknown interface kind %q in %q", parts[1], s)
		}
		return Name{Package: parts[0], Kind: k, Type: parts[2]}, nil
	default:
		return Name{}, fmt.Errorf("malformed interface name %q", s)
	}
}

// RequestName returns the name of the implicit request message of a service.
// rosidl names it `<Type>_Request` under the srv kind.
func (n Name) RequestName() Name {
	return Name{Package: n.Package, Kind: KindSrv, Type: n.Type + "_Request"}
}

// ResponseName returns the name of the implicit response message of a service.
func (n Name) ResponseName() Name {
	return Name{Package: n.Package, Kind: KindSrv, Type: n.Type + "_Response"}
}
