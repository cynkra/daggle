package cli

import (
	"fmt"
	"os"
	"os/user"

	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var annotateAuthor string

var annotateCmd = &cobra.Command{
	Use:   "annotate <dag> <run-id> <note>",
	Short: "Attach a human-readable note to a run",
	Long:  "Record a run_annotated event. Shows up in status, API, and UI for post-mortems.",
	Args:  cobra.ExactArgs(3),
	RunE:  annotateRun,
}

func init() {
	annotateCmd.Flags().StringVar(&annotateAuthor, "author", "", "override the author (default: $USER)")
	rootCmd.AddCommand(annotateCmd)
}

func annotateRun(_ *cobra.Command, args []string) error {
	dagName, runID, note := args[0], args[1], args[2]
	if note == "" {
		return fmt.Errorf("note is required")
	}

	run, err := state.FindRun(dagName, runID)
	if err != nil {
		return err
	}

	author := annotateAuthor
	if author == "" {
		author = currentUser()
	}

	w := state.NewEventWriter(run.Dir)
	defer func() { _ = w.Close() }()
	if err := w.Write(state.Event{
		Type:   state.EventRunAnnotated,
		Note:   note,
		Author: author,
	}); err != nil {
		return fmt.Errorf("write annotation: %w", err)
	}

	fmt.Printf("Annotated run %s/%s as %s\n", dagName, runID, author)
	return nil
}

// currentUser returns the best available identifier for the current user,
// falling back from os/user to $USER env to "unknown".
func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "unknown"
}
