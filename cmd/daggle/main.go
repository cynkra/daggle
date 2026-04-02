package main

import (
	"os"

	"github.com/cynkra/daggle/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
