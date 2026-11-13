//go:build !windows

package main

import "os"

// enableColor reports whether the terminal shows ANSI color codes; outside
// Windows every terminal does.
func enableColor(*os.File) bool { return true }
