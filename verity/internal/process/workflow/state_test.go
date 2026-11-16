package workflow

import "testing"

func TestTransitionRejectsInvalidTransition(t *testing.T) {
	if _, err := Transition(Draft, CandidateReady); err == nil {
		t.Fatal("expected invalid transition to be rejected")
	}
}

func TestWorkflowCanReachPostDeploySuccess(t *testing.T) {
	state := Draft
	steps := []struct {
		event Event
		want  State
	}{
		{ContractApproved, Ready}, {Start, Running}, {CandidateReady, Verifying},
		{ChecksPassed, WaitingApproval}, {ApprovalGranted, Deploying},
		{DeploymentDone, Verifying}, {PostDeployPassed, Succeeded},
	}
	for _, step := range steps {
		got, err := Transition(state, step.event)
		if err != nil {
			t.Fatalf("Transition(%q, %q): %v", state, step.event, err)
		}
		if got != step.want {
			t.Fatalf("Transition(%q, %q) = %q, want %q", state, step.event, got, step.want)
		}
		state = got
	}
}
