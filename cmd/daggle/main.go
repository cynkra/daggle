package main

import (
	"os"

	"github.com/cynkra/daggle/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
