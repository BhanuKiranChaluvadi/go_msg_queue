// Package cli holds small command-line helpers shared by the reader, server,
// and writer entry points. It centralises how usage is rendered and how stray
// positional arguments are handled so all three binaries behave consistently,
// while each command supplies its own wording.
package cli

import (
	"flag"
	"fmt"
	"os"
)

// SetUsage installs a flag.Usage that prints header, then the registered flag
// defaults, then footer. Sharing the layout here (and leaving the wording to
// each command) gives every binary a consistent -h/-help format.
func SetUsage(header, footer string) {
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprint(out, header)
		flag.PrintDefaults()
		fmt.Fprint(out, footer)
	}
}

// HandleExtraArgs enforces that no positional arguments were given. A lone
// "help" argument prints usage and exits 0, so `prog help` behaves like
// `prog -h`; any other stray argument prints usage and exits 2. Call it once,
// immediately after flag.Parse.
func HandleExtraArgs() {
	args := flag.Args()
	if len(args) == 0 {
		return
	}
	if args[0] == "help" {
		flag.Usage()
		os.Exit(0)
	}
	fmt.Fprintf(flag.CommandLine.Output(), "unexpected argument %q\n\n", args[0])
	flag.Usage()
	os.Exit(2)
}
