package cli

import (
	"testing"
)

func TestVersionCmd(t *testing.T) {
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}
