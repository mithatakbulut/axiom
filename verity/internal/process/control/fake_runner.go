package control

import (
	"context"
	"fmt"
)

// Activities supplies deterministic task outcomes and approval decisions for tests and demos.
// Production runtimes are introduced in a later roadmap phase.
type Activities interface {
	Run(context.Context, Task) (bool, error)
	Approve(context.Context, Task) (bool, error)
}

// RunWithActivities drives one run to a terminal or approval-blocked state.
// The definition's transition budget bounds the loop even if state is corrupted.
func RunWithActivities(ctx context.Context, r *Run, activities Activities) error {
	if activities == nil {
		return fmt.Errorf("activities are required")
	}
	if r.State == Ready || r.State == Paused {
		if err := r.Start(); err != nil {
			return err
		}
	} else if r.State != Running && r.State != Waiting {
		return fmt.Errorf("cannot run activities for run in state %s", r.State)
	}
	for r.State == Running || r.State == Waiting {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.State == Waiting {
			var task *Task
			for i := range r.Tasks {
				if r.Tasks[i].State == TaskWaiting {
					task = &r.Tasks[i]
					break
				}
			}
			if task == nil {
				return fmt.Errorf("run waits without an approval task")
			}
			approved, err := activities.Approve(ctx, *task)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if err != nil {
				return err
			}
			if err = r.Approve(task.ID, approved); err != nil {
				return err
			}
			continue
		}
		ready, err := r.Schedule()
		if err != nil {
			return err
		}
		if len(ready) == 0 {
			if r.State == Running {
				return fmt.Errorf("run has no schedulable tasks but is not terminal")
			}
			continue
		}
		for _, id := range ready {
			if r.State != Running {
				break
			}
			if err = r.BeginTask(id); err != nil {
				return err
			}
			var task Task
			for _, candidate := range r.Tasks {
				if candidate.ID == id {
					task = candidate
					break
				}
			}
			ok, activityErr := activities.Run(ctx, task)
			if err = ctx.Err(); err != nil {
				return err
			}
			if activityErr != nil {
				ok = false
			}
			if err = r.FinishTask(id, ok); err != nil {
				return err
			}
		}
	}
	return nil
}
