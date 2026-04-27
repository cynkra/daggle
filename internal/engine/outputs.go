package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cynkra/daggle/internal/executor"
)

func (e *Engine) collectOutputs(stepID string, outputs map[string]string) {
	if len(outputs) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	prefix := "DAGGLE_OUTPUT_" + strings.ToUpper(strings.ReplaceAll(stepID, "-", "_")) + "_"
	for k, v := range outputs {
		key := prefix + strings.ToUpper(k)
		e.outputs[key] = v
		e.logger.Info("captured output", "step", stepID, "key", k, "value", e.redact(v))
	}
}

// writeSummaryFile writes concatenated summary content to {step.ID}.summary.md.
func (e *Engine) writeSummaryFile(stepID string, summaries []executor.Summary) {
	if len(summaries) == 0 {
		return
	}
	var parts []string
	for _, s := range summaries {
		parts = append(parts, s.Content)
	}
	path := filepath.Join(e.runInfo.Dir, stepID+".summary.md")
	if err := os.WriteFile(path, []byte(strings.Join(parts, "\n")), 0o644); err != nil {
		e.logger.Warn("failed to write summary file", "step", stepID, "error", e.redactErr(err))
	}
}

// writeMetadataFile writes metadata entries as a JSON array to {step.ID}.meta.json.
func (e *Engine) writeMetadataFile(stepID string, metadata []executor.MetaEntry) {
	if len(metadata) == 0 {
		return
	}
	type metaJSON struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	entries := make([]metaJSON, len(metadata))
	for i, m := range metadata {
		entries[i] = metaJSON{Name: m.Name, Type: m.Type, Value: m.Value}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		e.logger.Warn("failed to marshal metadata", "step", stepID, "error", e.redactErr(err))
		return
	}
	path := filepath.Join(e.runInfo.Dir, stepID+".meta.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		e.logger.Warn("failed to write metadata file", "step", stepID, "error", e.redactErr(err))
	}
}

// writeValidationFile writes validation results as a JSON array to {step.ID}.validations.json.
func (e *Engine) writeValidationFile(stepID string, validations []executor.ValidationResult) {
	if len(validations) == 0 {
		return
	}
	type validationJSON struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	entries := make([]validationJSON, len(validations))
	for i, v := range validations {
		entries[i] = validationJSON{Name: v.Name, Status: v.Status, Message: v.Message}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		e.logger.Warn("failed to marshal validations", "step", stepID, "error", e.redactErr(err))
		return
	}
	path := filepath.Join(e.runInfo.Dir, stepID+".validations.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		e.logger.Warn("failed to write validations file", "step", stepID, "error", e.redactErr(err))
	}
}

// checkValidationFailures returns an error if any validation result has status "fail".
func checkValidationFailures(validations []executor.ValidationResult) error {
	var failed []string
	for _, v := range validations {
		if v.Status == "fail" {
			failed = append(failed, v.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("validation failed: %s", strings.Join(failed, ", "))
	}
	return nil
}
