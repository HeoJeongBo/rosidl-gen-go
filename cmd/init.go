package cmd

import (
	"fmt"
	"os"
)

// runInit writes a commented skeleton to stdout and never touches a file.
//
// Config files in this shape end up carrying rationale — why a path wins, why
// an interface is bound externally — so a scaffolder that wrote or rewrote one
// would eventually destroy the part worth keeping. Redirect it yourself:
//
//	rosidl-gen init > rosidl-gen.yaml
func runInit(args []string) error {
	fs := newFlagSet("init", "init")
	if err := parse(fs, args, 0); err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, skeleton)
	return nil
}

const skeleton = `# rosidl-gen: ROS 2 .msg/.srv -> Go structs whose field order is the CDR wire
# layout. Run the generator after changing this file or any definition it
# reads, and commit the result; ` + "`-check`" + ` verifies it in CI.
#
# Every path below is resolved relative to THIS file.

# Where generated files go, and the Go package name they declare.
out: internal/ros
package: ros

# Directories scanned for ament packages (a directory holding package.xml, or
# a directory of such directories — the scan is one level deep, no further).
# Earlier entries win, so a locally checked-in definition beats an installed
# one. ${VAR} is expanded; an entry whose variable is unset is skipped, which
# is what makes the ROS install optional on a machine without one.
search_paths:
  - msg
  - ${ROS_ROOT}/share

# Interfaces to emit. Nested dependencies are pulled in automatically, so list
# only what you talk to directly.
#   my_msgs/**            every interface in the package
#   my_msgs/msg/*         every message
#   my_msgs/msg/Thing     one interface
generate:
  - my_msgs/**

# Optional: bind an interface to a Go type that already exists instead of
# generating one. A qualified value resolves through ` + "`imports`" + `; an
# unqualified one must be hand-written in the output package.
#
# imports:
#   ros: github.com/lesomnus/cdr/ros
# external:
#   std_msgs/msg/Header: ros.Header

# Optional: override a derived Go type name. Only needed to break a collision
# between two packages that declare the same type name.
#
# rename:
#   other_msgs/msg/Thing: OtherThing

# Optional emission toggles.
#
# emit:
#   as_error: true   # AsError() on responses carrying success + message

# Optional: mirror a C++ topic/service name header into Go constants.
#
# names:
#   header: include/names.h
#   out: internal/ros/names.g.go
`
