package main

import (
	"os"

	"github.com/schochastics/rdag/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
