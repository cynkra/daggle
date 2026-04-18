package cli

// dashIfEmpty renders an empty string as "-" for tabular CLI output.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
