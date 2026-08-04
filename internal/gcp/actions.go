package gcp

import (
	"context"
	"fmt"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// Action is something g9s can do to a resource that changes it.
//
// This is the only place in the package that is not a list or a get, and it is
// kept deliberately small. Every action here is one call against one named
// resource: there is no bulk form, no "all instances matching", and no way to
// reach one without the resource already being on screen and selected. That is
// a product boundary, not an implementation limit — a support engineer holding
// a tool that can change ten things at once during an incident is how a bad
// afternoon becomes a bad quarter.
type Action struct {
	// ID is the stable identifier, used for key binding and in the audit line.
	ID string
	// Verb is what the confirmation says is about to happen.
	Verb string
	// Description explains the consequence, in the confirmation, in one line.
	Description string
	// Destructive marks actions that interrupt something that is working.
	//
	// It controls how hard the confirmation is: a destructive action requires
	// the resource's own name to be typed, because a yes/no prompt answered by
	// reflex is not a decision. Starting a stopped instance is not destructive;
	// stopping a running one is, and resetting one is worse than stopping it.
	Destructive bool
}

// VM actions. Deliberately three, and deliberately not delete: a stopped
// instance can be started again, while a deleted one takes its boot disk with
// it unless someone thought about auto-delete beforehand. Nothing in a
// read-mostly console should be able to do that.
var (
	ActionStartVM = Action{
		ID:          "start",
		Verb:        "Start",
		Description: "boots the instance; it begins billing for CPU and memory again",
	}
	ActionStopVM = Action{
		ID:          "stop",
		Verb:        "Stop",
		Description: "shuts the guest down cleanly and stops it serving; anything on it goes offline",
		Destructive: true,
	}
	ActionResetVM = Action{
		ID:   "reset",
		Verb: "Reset",
		Description: "cuts power without telling the guest — like pulling the plug; " +
			"unwritten data is lost and filesystems may need repair",
		Destructive: true,
	}
)

// ActionsFor returns what can be done to a resource, given its current state.
//
// State-aware on purpose: offering "stop" on an instance that is already
// TERMINATED is an affordance that does nothing, and an action that silently
// does nothing is indistinguishable from one that failed.
func ActionsFor(r Resource) []Action {
	inst, ok := r.Raw.(*computepb.Instance)
	if !ok {
		return nil
	}

	switch strings.ToUpper(inst.GetStatus()) {
	case "RUNNING":
		return []Action{ActionStopVM, ActionResetVM}
	case "TERMINATED", "STOPPED", "SUSPENDED":
		return []Action{ActionStartVM}
	default:
		// STAGING, STOPPING, PROVISIONING and the rest are already in motion.
		// Adding another instruction to an instance mid-transition is how you
		// get a state nobody asked for.
		return nil
	}
}

// ActionTarget is the exact resource an action will be applied to.
//
// Resolved once, from the row, and carried through the confirmation unchanged
// so that what is confirmed and what is executed cannot differ. The table
// underneath keeps refreshing; reading the target back out of it at execution
// time would mean confirming one instance and stopping another.
type ActionTarget struct {
	Name string
	Zone string
}

// ResolveActionTarget extracts and validates the target of an action.
//
// The same shape check SSHTarget makes, for the same reason: these values go
// into an API call, and GCP's own naming rules are what guarantee they are not
// something else in disguise. A name that could not have been minted by GCP is
// refused rather than sent.
func ResolveActionTarget(r Resource) (ActionTarget, bool) {
	inst, ok := r.Raw.(*computepb.Instance)
	if !ok {
		return ActionTarget{}, false
	}
	name, zone := inst.GetName(), lastSegment(inst.GetZone())
	if !resourceNamePattern.MatchString(name) || !resourceNamePattern.MatchString(zone) {
		return ActionTarget{}, false
	}
	return ActionTarget{Name: name, Zone: zone}, true
}

// RunAction performs one action against one instance.
//
// It waits for the operation to be accepted, not for the instance to reach its
// new state: an instance takes tens of seconds to stop, and holding the UI
// through that would either block the whole program or invent a progress model
// for something the next refresh already reports accurately.
func RunAction(ctx context.Context, p config.Project, action Action, target ActionTarget, opts []option.ClientOption) error {
	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("compute client: %w", err)
	}
	defer client.Close()

	switch action.ID {
	case ActionStartVM.ID:
		_, err = client.Start(ctx, &computepb.StartInstanceRequest{
			Project: p.ProjectID, Zone: target.Zone, Instance: target.Name,
		})
	case ActionStopVM.ID:
		_, err = client.Stop(ctx, &computepb.StopInstanceRequest{
			Project: p.ProjectID, Zone: target.Zone, Instance: target.Name,
		})
	case ActionResetVM.ID:
		_, err = client.Reset(ctx, &computepb.ResetInstanceRequest{
			Project: p.ProjectID, Zone: target.Zone, Instance: target.Name,
		})
	default:
		// An unrecognised action reaching here is a programming error, and
		// guessing which call was meant is the worst possible response to it.
		return fmt.Errorf("unknown action %q", action.ID)
	}
	if err != nil {
		return err
	}
	return nil
}
