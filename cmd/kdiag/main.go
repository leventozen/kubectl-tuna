package main

import (
	"fmt"
	"os"

	"github.com/leventozen/kdiag/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.NewRootCommand(version).Execute(); err != nil {
		if code, ok := cli.ExitCode(err); ok {
			os.Exit(code)
		}
		fmt.Fprintf(os.Stderr, "kdiag: %v\n", err)
		os.Exit(1)
	}
}
