package control

import "testing"

func TestValidatePlan(t *testing.T) {
	valid := func() Definition {
		return Definition{SchemaVersion: 1, ID: "plan", Name: "Plan", Version: 1,
			Goal: "Deliver", AcceptanceCriteria: []string{"Checks pass"}, SourcePaths: []string{"src"},
			MaxTasks: 3, MaxAttempts: 2, MaxTransitions: 32, Tasks: []Task{
				{ID: "one", Title: "First", Description: "Implement", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, TimeoutSeconds: 60, MaxAttempts: 2},
				{ID: "approval", Title: "Approve", Description: "Review", AcceptanceCriteria: []string{"Approved"}, Approval: true, DependsOn: []string{"one"}, MaxAttempts: 1},
			}}
	}
	if err := valid().ValidatePlan(); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Definition){
		"goal":               func(d *Definition) { d.Goal = " " },
		"criteria":           func(d *Definition) { d.AcceptanceCriteria = nil },
		"scope":              func(d *Definition) { d.Tasks[0].SourcePaths = nil },
		"timeout":            func(d *Definition) { d.Tasks[0].TimeoutSeconds = 0 },
		"missing dependency": func(d *Definition) { d.Tasks[0].DependsOn = []string{"missing"} },
		"cycle":              func(d *Definition) { d.Tasks[0].DependsOn = []string{"approval"} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			d := valid()
			mutate(&d)
			if err := d.ValidatePlan(); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}
