package cli

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

func TestSetUsagePrintsHeaderFlagsFooter(t *testing.T) {
	oldCmd, oldUsage := flag.CommandLine, flag.Usage
	t.Cleanup(func() { flag.CommandLine, flag.Usage = oldCmd, oldUsage })

	flag.CommandLine = flag.NewFlagSet("prog", flag.ContinueOnError)
	var buf bytes.Buffer
	flag.CommandLine.SetOutput(&buf)
	flag.String("name", "world", "who to greet")

	SetUsage("HEADER\n", "FOOTER\n")
	flag.Usage()

	got := buf.String()
	for _, want := range []string{"HEADER", "-name", "who to greet", "FOOTER"} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage output missing %q\nfull output:\n%s", want, got)
		}
	}
}
