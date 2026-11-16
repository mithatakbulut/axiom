package domain

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"verity/internal/process/workflow"
)

const SchemaVersion = 1

var ErrInvalidModel = errors.New("invalid domain model")

type Project struct {
	ID            ID        `json:"id"`
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"name"`
	Root          string    `json:"root"`
	CreatedAt     time.Time `json:"created_at"`
}
type Workflow struct {
	ID            ID             `json:"id"`
	ProjectID     ID             `json:"project_id"`
	SchemaVersion int            `json:"schema_version"`
	Name          string         `json:"name"`
	State         workflow.State `json:"state"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}
type Task struct {
	ID            ID     `json:"id"`
	WorkflowID    ID     `json:"workflow_id"`
	SchemaVersion int    `json:"schema_version"`
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	DependsOn     []ID   `json:"depends_on,omitempty"`
}
type Candidate struct {
	ID            ID        `json:"id"`
	WorkflowID    ID        `json:"workflow_id"`
	SchemaVersion int       `json:"schema_version"`
	Revision      string    `json:"revision"`
	CreatedAt     time.Time `json:"created_at"`
}
type Policy struct {
	ID            ID              `json:"id"`
	SchemaVersion int             `json:"schema_version"`
	Name          string          `json:"name"`
	Rules         json.RawMessage `json:"rules"`
}
type WorkflowEvent struct {
	ID            ID             `json:"id"`
	WorkflowID    ID             `json:"workflow_id"`
	SchemaVersion int            `json:"schema_version"`
	Sequence      int64          `json:"sequence"`
	From          workflow.State `json:"from"`
	To            workflow.State `json:"to"`
	Event         workflow.Event `json:"event"`
	CorrelationID string         `json:"correlation_id"`
	Actor         string         `json:"actor"`
	Reason        string         `json:"reason,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

func (p Project) Validate() error {
	if p.ID.Validate("prj") != nil || p.SchemaVersion != SchemaVersion || p.Name == "" || p.Root == "" || p.CreatedAt.IsZero() {
		return ErrInvalidModel
	}
	return nil
}
func (w Workflow) Validate() error {
	if w.ID.Validate("wf") != nil || w.ProjectID.Validate("prj") != nil || w.SchemaVersion != SchemaVersion || w.Name == "" || w.State == "" {
		return ErrInvalidModel
	}
	return nil
}
func (t Task) Validate() error {
	if t.ID.Validate("task") != nil || t.WorkflowID.Validate("wf") != nil || t.SchemaVersion != SchemaVersion || t.Title == "" {
		return ErrInvalidModel
	}
	for _, id := range t.DependsOn {
		if id.Validate("task") != nil {
			return ErrInvalidModel
		}
	}
	return nil
}
func (c Candidate) Validate() error {
	if c.ID.Validate("cand") != nil || c.WorkflowID.Validate("wf") != nil || c.SchemaVersion != SchemaVersion || c.CreatedAt.IsZero() || (len(c.Revision) != 40 && len(c.Revision) != 64) {
		return ErrInvalidModel
	}
	if _, err := hex.DecodeString(c.Revision); err != nil {
		return ErrInvalidModel
	}
	return nil
}
func (p Policy) Validate() error {
	if p.ID.Validate("pol") != nil || p.SchemaVersion != SchemaVersion || p.Name == "" || !json.Valid(p.Rules) {
		return ErrInvalidModel
	}
	return nil
}
