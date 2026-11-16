// Package workflow owns deterministic delivery workflow state transitions.
package workflow

import "fmt"

type State string

const (
	Draft           State = "DRAFT"
	Ready           State = "READY"
	Running         State = "RUNNING"
	WaitingApproval State = "WAITING_APPROVAL"
	Blocked         State = "BLOCKED"
	Verifying       State = "VERIFYING"
	Deploying       State = "DEPLOYING"
	Succeeded       State = "SUCCEEDED"
	Failed          State = "FAILED"
	Cancelled       State = "CANCELLED"
	Superseded      State = "SUPERSEDED"
)

type Event string

const (
	ContractApproved Event = "contract_approved"
	Start            Event = "start"
	NeedsApproval    Event = "needs_approval"
	ApprovalGranted  Event = "approval_granted"
	ApprovalRejected Event = "approval_rejected"
	ExternalBlocker  Event = "external_blocker"
	CandidateReady   Event = "candidate_ready"
	ChecksFailed     Event = "checks_failed"
	ChecksPassed     Event = "checks_passed"
	DeploymentDone   Event = "deployment_succeeded"
	PostDeployPassed Event = "post_deploy_checks_passed"
	Cancel           Event = "cancel"
	Supersede        Event = "supersede"
)

var transitions = map[State]map[Event]State{
	Draft:           {ContractApproved: Ready, Cancel: Cancelled, Supersede: Superseded},
	Ready:           {Start: Running, Cancel: Cancelled, Supersede: Superseded},
	Running:         {NeedsApproval: WaitingApproval, ExternalBlocker: Blocked, CandidateReady: Verifying, Cancel: Cancelled, Supersede: Superseded},
	WaitingApproval: {Start: Running, ApprovalGranted: Deploying, ApprovalRejected: Failed, Cancel: Cancelled, Supersede: Superseded},
	Blocked:         {Start: Running, Cancel: Cancelled, Supersede: Superseded},
	Verifying:       {ChecksFailed: Running, ChecksPassed: WaitingApproval, PostDeployPassed: Succeeded, Cancel: Cancelled, Supersede: Superseded},
	Deploying:       {DeploymentDone: Verifying, Cancel: Cancelled, Supersede: Superseded},
}

// Transition returns the next state or an error without changing the input.
func Transition(current State, event Event) (State, error) {
	if next, ok := transitions[current][event]; ok {
		return next, nil
	}
	if current == Succeeded || current == Failed || current == Cancelled || current == Superseded {
		return "", fmt.Errorf("workflow state %q is terminal", current)
	}
	return "", fmt.Errorf("event %q is not valid from workflow state %q", event, current)
}
