// Command rosidl-gen generates Go types from ROS2 interface definitions and
// mirrors the C++ topic/service name header into Go.
//
// A Go program talks DDS by CDR-encoding plain structs, and that codec is
// positional: struct field order IS the wire contract. Hand-mirroring a .msg
// therefore breaks silently when a field is inserted upstream. This generator
// removes the hand step.
//
// Usage:
//
//	go run . -config rosidl-gen.yaml
//	go run . -config rosidl-gen.yaml -check
package main

import "github.com/HeoJeongBo/rosidl-gen-go/cmd"

func main() {
	cmd.Main()
}
