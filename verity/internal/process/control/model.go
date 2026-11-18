// Package control implements deterministic, agent-independent workflow runs.
package control

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type WorkflowState string

const (
	Draft     WorkflowState = "DRAFT"
	Ready     WorkflowState = "READY"
	Running   WorkflowState = "RUNNING"
	Paused    WorkflowState = "PAUSED"
	Waiting   WorkflowState = "WAITING_APPROVAL"
	Succeeded WorkflowState = "SUCCEEDED"
	Failed    WorkflowState = "FAILED"
	Cancelled WorkflowState = "CANCELLED"
)

type TaskState string

const (
	Pending       TaskState = "PENDING"
	TaskReady     TaskState = "READY"
	TaskRunning   TaskState = "RUNNING"
	TaskWaiting   TaskState = "WAITING_APPROVAL"
	TaskCompleted TaskState = "COMPLETED"
	TaskFailed    TaskState = "FAILED"
	TaskCancelled TaskState = "CANCELLED"
)

type Task struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Description        string    `json:"description,omitempty"`
	AcceptanceCriteria []string  `json:"acceptance_criteria,omitempty"`
	SourcePaths        []string  `json:"source_paths,omitempty"`
	TimeoutSeconds     int       `json:"timeout_seconds,omitempty"`
	DependsOn          []string  `json:"depends_on,omitempty"`
	Approval           bool      `json:"approval,omitempty"`
	ApprovalActor      string    `json:"approval_actor,omitempty"`
	ApprovalReason     string    `json:"approval_reason,omitempty"`
	MaxAttempts        int       `json:"max_attempts"`
	Attempts           int       `json:"attempts"`
	State              TaskState `json:"state"`
}
type Definition struct {
	SchemaVersion        int      `json:"schema_version"`
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Goal                 string   `json:"goal,omitempty"`
	AcceptanceCriteria   []string `json:"acceptance_criteria,omitempty"`
	SourcePaths          []string `json:"source_paths,omitempty"`
	VerificationManifest string   `json:"verification_manifest,omitempty"`
	Version              int      `json:"version"`
	Tasks                []Task   `json:"tasks"`
	MaxTasks             int      `json:"max_tasks"`
	MaxAttempts          int      `json:"max_attempts"`
	MaxTransitions       int      `json:"max_transitions"`
}

// ValidatePlan applies the stricter requirements for explicitly authored plans.
// Existing workflow templates continue to use Validate.
func (d Definition) ValidatePlan() error {
	if err := d.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(d.Goal) == "" || !nonempty(d.AcceptanceCriteria) || !nonempty(d.SourcePaths) {
		return fmt.Errorf("%w: plan requires a goal, acceptance criteria and source paths", ErrInvalid)
	}
	for _, path := range d.SourcePaths {
		if !validPlanPath(path) {
			return fmt.Errorf("%w: invalid plan source path %q", ErrInvalid, path)
		}
	}
	for _, t := range d.Tasks {
		if strings.TrimSpace(t.Description) == "" || !nonempty(t.AcceptanceCriteria) {
			return fmt.Errorf("%w: task %q requires a description and acceptance criteria", ErrInvalid, t.ID)
		}
		if !t.Approval && (t.TimeoutSeconds <= 0 || t.TimeoutSeconds > 86400 || !nonempty(t.SourcePaths)) {
			return fmt.Errorf("%w: task %q requires a timeout and source paths", ErrInvalid, t.ID)
		}
		for _, path := range t.SourcePaths {
			if !validPlanPath(path) {
				return fmt.Errorf("%w: invalid task source path %q", ErrInvalid, path)
			}
			covered := false
			for _, overall := range d.SourcePaths {
				rel, err := filepath.Rel(filepath.Clean(overall), filepath.Clean(path))
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					covered = true
					break
				}
			}
			if !covered {
				return fmt.Errorf("%w: task %q source path %q is outside plan scope", ErrInvalid, t.ID, path)
			}
		}
	}
	return nil
}

func validPlanPath(path string) bool {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func nonempty(items []string) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			return false
		}
	}
	return true
}

type Run struct {
	ID                string        `json:"id"`
	DefinitionID      string        `json:"definition_id"`
	DefinitionVersion int           `json:"definition_version"`
	Definition        Definition    `json:"definition_snapshot"`
	State             WorkflowState `json:"state"`
	Tasks             []Task        `json:"tasks"`
	Transitions       int           `json:"transitions"`
	CreatedAt         time.Time     `json:"created_at"`
}
type Lease struct {
	RunID     string    `json:"run_id"`
	Owner     string    `json:"owner"`
	ExpiresAt time.Time `json:"expires_at"`
}

var ErrInvalid = errors.New("invalid workflow control model")

func (d Definition) Validate() error {
	if d.SchemaVersion != 1 || d.ID == "" || d.Name == "" || d.Version < 1 || d.MaxTasks < 1 || d.MaxAttempts < 1 || d.MaxTransitions < 1 || len(d.Tasks) == 0 || len(d.Tasks) > d.MaxTasks {
		return ErrInvalid
	}
	byID := map[string]Task{}
	for _, t := range d.Tasks {
		if t.ID == "" || t.Title == "" || t.MaxAttempts < 1 || t.MaxAttempts > d.MaxAttempts {
			return fmt.Errorf("%w: invalid task %q or retry bound", ErrInvalid, t.ID)
		}
		if _, ok := byID[t.ID]; ok {
			return fmt.Errorf("%w: duplicate task %q", ErrInvalid, t.ID)
		}
		byID[t.ID] = t
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("%w: dependency cycle at %q", ErrInvalid, id)
		}
		if done[id] {
			return nil
		}
		t, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: unknown task %q", ErrInvalid, id)
		}
		visiting[id] = true
		for _, dep := range t.DependsOn {
			if _, ok := byID[dep]; !ok {
				return fmt.Errorf("%w: task %q depends on unknown %q", ErrInvalid, id, dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, id)
		done[id] = true
		return nil
	}
	for id := range byID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func NewRun(id string, d Definition, now time.Time) (Run, error) {
	if err := d.Validate(); err != nil {
		return Run{}, err
	}
	tasks := append([]Task(nil), d.Tasks...)
	for i := range tasks {
		tasks[i].State = Pending
		tasks[i].Attempts = 0
		tasks[i].DependsOn = append([]string(nil), tasks[i].DependsOn...)
	}
	return Run{ID: id, DefinitionID: d.ID, DefinitionVersion: d.Version, Definition: d, State: Ready, Tasks: tasks, CreatedAt: now.UTC()}, nil
}
func (r *Run) advance() error {
	if r.Transitions >= r.Definition.MaxTransitions {
		return fmt.Errorf("scheduling transition budget exhausted")
	}
	r.Transitions++
	return nil
}
func (r *Run) Start() error {
	if r.State != Ready && r.State != Paused {
		return fmt.Errorf("cannot start run from %s", r.State)
	}
	if err := r.advance(); err != nil {
		return err
	}
	r.State = Running
	return nil
}
func (r *Run) Pause() error {
	if r.State != Running {
		return fmt.Errorf("cannot pause run from %s", r.State)
	}
	if err := r.advance(); err != nil {
		return err
	}
	r.State = Paused
	return nil
}
func (r *Run) Resume() error { return r.Start() }
func (r *Run) Cancel() error {
	if r.State == Succeeded || r.State == Failed || r.State == Cancelled {
		return fmt.Errorf("cannot cancel terminal run")
	}
	if err := r.advance(); err != nil {
		return err
	}
	r.State = Cancelled
	for i := range r.Tasks {
		if r.Tasks[i].State != TaskCompleted {
			r.Tasks[i].State = TaskCancelled
		}
	}
	return nil
}

// Schedule returns newly eligible task IDs once, in definition order. Completion is never inferred from execution.
func (r *Run) Schedule() ([]string, error) {
	if r.State != Running {
		return nil, fmt.Errorf("cannot schedule run from %s", r.State)
	}
	out := []string{}
	completed := map[string]bool{}
	for _, t := range r.Tasks {
		completed[t.ID] = t.State == TaskCompleted
	}
	for i := range r.Tasks {
		t := &r.Tasks[i]
		if t.State == TaskReady {
			out = append(out, t.ID)
			continue
		}
		if t.State != Pending {
			continue
		}
		ready := true
		for _, dep := range t.DependsOn {
			if !completed[dep] {
				ready = false
				break
			}
		}
		if ready {
			if err := r.advance(); err != nil {
				return out, err
			}
			if t.Approval {
				t.State = TaskWaiting
				r.State = Waiting
			} else {
				t.State = TaskReady
			}
			out = append(out, t.ID)
			if r.State == Waiting {
				break
			}
		}
	}
	return out, nil
}

// ScheduleOne selects the first eligible task in definition order, including
// approval tasks, without exposing later independent tasks to the scheduler.
func (r *Run) ScheduleOne() (string, error) {
	if r.State != Running {
		return "", fmt.Errorf("cannot schedule run from %s", r.State)
	}
	completed := map[string]bool{}
	for _, t := range r.Tasks {
		completed[t.ID] = t.State == TaskCompleted
	}
	for i := range r.Tasks {
		t := &r.Tasks[i]
		if t.State == TaskReady {
			return t.ID, nil
		}
		if t.State != Pending {
			continue
		}
		ready := true
		for _, dep := range t.DependsOn {
			if !completed[dep] {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		if err := r.advance(); err != nil {
			return "", err
		}
		if t.Approval {
			t.State, r.State = TaskWaiting, Waiting
		} else {
			t.State = TaskReady
		}
		return t.ID, nil
	}
	return "", nil
}
func (r *Run) Approve(taskID string, approved bool) error {
	if r.State != Waiting {
		return fmt.Errorf("run is not waiting for approval")
	}
	for i := range r.Tasks {
		t := &r.Tasks[i]
		if t.ID == taskID && t.State == TaskWaiting {
			if err := r.advance(); err != nil {
				return err
			}
			if approved {
				t.State = TaskCompleted
				r.State = Running
				all := true
				for _, task := range r.Tasks {
					if task.State != TaskCompleted {
						all = false
					}
				}
				if all {
					r.State = Succeeded
				}
			} else {
				t.State = TaskFailed
				r.State = Failed
			}
			return nil
		}
	}
	return fmt.Errorf("approval task not found")
}
func (r *Run) BeginTask(id string) error {
	if r.State != Running {
		return fmt.Errorf("run is not running")
	}
	for i := range r.Tasks {
		t := &r.Tasks[i]
		if t.ID == id && t.State == TaskReady {
			if t.Attempts >= t.MaxAttempts {
				return fmt.Errorf("task retry budget exhausted")
			}
			if err := r.advance(); err != nil {
				return err
			}
			t.Attempts++
			t.State = TaskRunning
			return nil
		}
	}
	return fmt.Errorf("task is not ready")
}
func (r *Run) FinishTask(id string, success bool) error {
	if r.State != Running {
		return fmt.Errorf("cannot finish task while run is %s", r.State)
	}
	for i := range r.Tasks {
		t := &r.Tasks[i]
		if t.ID == id && t.State == TaskRunning {
			if err := r.advance(); err != nil {
				return err
			}
			if success {
				t.State = TaskCompleted
			} else if t.Attempts < t.MaxAttempts {
				t.State = TaskReady
			} else {
				t.State = TaskFailed
				r.State = Failed
			}
			all := true
			for _, x := range r.Tasks {
				if x.State != TaskCompleted {
					all = false
				}
			}
			if all {
				r.State = Succeeded
			}
			return nil
		}
	}
	return fmt.Errorf("task is not running")
}
