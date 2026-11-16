package control

func Template(kind, id string) Definition {
	tasks := map[string][]Task{
		"feature":       {{ID: "spec", Title: "Define acceptance criteria", MaxAttempts: 2}, {ID: "implement", Title: "Implement feature", DependsOn: []string{"spec"}, MaxAttempts: 2}, {ID: "verify", Title: "Verify feature", DependsOn: []string{"implement"}, MaxAttempts: 1}, {ID: "approve", Title: "Approve delivery", DependsOn: []string{"verify"}, Approval: true, MaxAttempts: 1}},
		"bugfix":        {{ID: "reproduce", Title: "Reproduce defect", MaxAttempts: 2}, {ID: "fix", Title: "Implement fix", DependsOn: []string{"reproduce"}, MaxAttempts: 2}, {ID: "regression", Title: "Verify regression fix", DependsOn: []string{"fix"}, MaxAttempts: 1}},
		"documentation": {{ID: "draft", Title: "Update documentation", MaxAttempts: 2}, {ID: "review", Title: "Review documentation", DependsOn: []string{"draft"}, MaxAttempts: 1}},
	}[kind]
	return Definition{SchemaVersion: 1, ID: id, Name: kind, Version: 1, Tasks: tasks, MaxTasks: 32, MaxAttempts: 3, MaxTransitions: 256}
}
