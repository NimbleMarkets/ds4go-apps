package steerinspect

import (
	"encoding/json"
	"io"
	"time"
)

// ExportMetadata holds configuration and execution details of the exported run.
type ExportMetadata struct {
	ModelPath   string    `json:"model_path"`
	Date        time.Time `json:"date"`
	Temperature float32   `json:"temperature"`
	TopP        float32   `json:"top_p"`
	MinP        float32   `json:"min_p"`
	Seed        uint64    `json:"seed"`
	CtxSize     int       `json:"ctx_size"`
}

// LaneExport captures a single lane snapshot for serialization.
type LaneExport struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent_id,omitempty"`
	ParentPos int    `json:"parent_pos,omitempty"`
	Steps     []Step `json:"steps"`
}

// RunExport is the complete JSON structure of an exported workbench state.
type RunExport struct {
	Metadata ExportMetadata `json:"metadata"`
	Lanes    []LaneExport   `json:"lanes"`
}

// ExportJSON serializes all lanes, metrics, and parameters to a writer.
func (r *Runner) ExportJSON(w io.Writer, modelPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	meta := ExportMetadata{
		ModelPath:   modelPath,
		Date:        time.Now(),
		Temperature: r.Temperature,
		TopP:        r.TopP,
		MinP:        r.MinP,
		Seed:        r.Seed,
		CtxSize:     r.CtxSize,
	}

	lanes := make([]LaneExport, 0, len(r.Lanes))
	for _, l := range r.Lanes {
		lanes = append(lanes, LaneExport{
			ID:        l.ID,
			ParentID:  l.ParentID,
			ParentPos: l.ParentPos,
			Steps:     l.GetSteps(),
		})
	}

	exp := RunExport{
		Metadata: meta,
		Lanes:    lanes,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(exp)
}
