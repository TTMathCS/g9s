package gcp

import (
	"context"
	"sort"
	"sync"
	"time"

	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// fleetConcurrency bounds how many projects are fetched at once.
//
// Four, not one per project. A fleet fetch multiplies out as projects × the
// regions each kind sweeps, so ten projects against a kind that fans out over
// six regions is sixty concurrent calls if nothing holds it back — enough to
// hit per-minute quotas on a service nobody was having trouble with, and
// enough to make a corporate proxy the bottleneck for everything else on the
// machine. Four keeps a ten-project sweep to a few seconds without turning one
// keypress into a burst.
const fleetConcurrency = 4

// ProjectSnapshot is one project's answer for one kind.
//
// The metadata is the point. A fleet table assembled from ten of these is only
// honest if each one still knows whether it was complete, who it was read as,
// and when — a row count summed across projects where two were denied and one
// was capped is a number that means nothing, and nothing downstream can tell
// unless the snapshot carries it.
type ProjectSnapshot struct {
	// Project is the configured project this came from.
	Project config.Project
	// Result is what that project returned, warnings and all.
	Result Result
	// Err is set when the listing failed outright, as opposed to returning
	// partial results with warnings. A project that could not be read at all
	// contributes no rows, and saying so is the difference between "there are
	// none here" and "we never found out".
	Err error
	// FetchedAt is when this snapshot was taken.
	FetchedAt time.Time
	// Skipped marks a project that was not attempted — no credentials, or a
	// credential that will not mint a token. Distinct from Err because nothing
	// went wrong; the project was simply not available to ask.
	Skipped bool
	// SkipReason says why, in words a reader can act on.
	SkipReason string
}

// Complete reports whether this project's answer is the whole picture.
func (s ProjectSnapshot) Complete() bool {
	return s.Err == nil && !s.Skipped && s.Result.Complete()
}

// FleetResult is one kind read across many projects.
type FleetResult struct {
	Kind      Kind
	Snapshots []ProjectSnapshot
}

// Resources flattens every project's rows into one slice, each stamped with
// the project it came from.
func (f FleetResult) Resources() []Resource {
	var out []Resource
	for _, s := range f.Snapshots {
		for _, r := range s.Result.Resources {
			r.Project = s.Project.Name
			out = append(out, r)
		}
	}
	return out
}

// Counts summarises how the sweep went, for the progress line.
func (f FleetResult) Counts() (complete, partial, failed, skipped int) {
	for _, s := range f.Snapshots {
		switch {
		case s.Skipped:
			skipped++
		case s.Err != nil:
			failed++
		case !s.Result.Complete():
			partial++
		default:
			complete++
		}
	}
	return complete, partial, failed, skipped
}

// Trustworthy reports whether the flattened rows can be counted on.
//
// False as soon as any project failed, was skipped or came back partial. This
// is deliberately strict: the whole hazard of a fleet view is that it looks
// like an answer about the estate when it is an answer about the projects that
// happened to respond, and a caller asking "is this the whole picture" should
// have to opt into the nuance rather than out of it.
func (f FleetResult) Trustworthy() bool {
	complete, partial, failed, skipped := f.Counts()
	return complete > 0 && partial == 0 && failed == 0 && skipped == 0
}

// FleetLister is what the coordinator needs from a project to read it.
//
// An interface rather than the Manager so the coordinator can be tested
// without credentials, and so the decision about whether a project is usable
// stays with the caller that already tracks it.
type FleetLister interface {
	// Usable reports whether this project can be read right now, and why not
	// when it cannot.
	Usable(p config.Project) (ok bool, reason string)
	// Options returns the client options for a project.
	Options(p config.Project) []option.ClientOption
}

// SweepFleet reads one kind across many projects, with bounded concurrency.
//
// Every project produces a snapshot, including the ones that were skipped or
// failed. A coordinator that silently dropped them would hand back a table
// that looks like the estate and is actually the subset that answered, which
// is the single most dangerous thing a cross-project view can do.
//
// The context bounds the whole sweep. Cancelling it stops the projects that
// have not started and lets the in-flight ones fail on their own deadline,
// which is what makes leaving the screen cheap rather than something that goes
// on costing API calls.
func SweepFleet(ctx context.Context, cfg *config.Config, lister Lister, projects []config.Project, access FleetLister) FleetResult {
	out := FleetResult{Kind: lister.Kind(), Snapshots: make([]ProjectSnapshot, len(projects))}

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, fleetConcurrency)
	)
	for i, p := range projects {
		// Indexed writes rather than an appending mutex: the snapshot order
		// then matches the configured project order regardless of which
		// finished first, so the table does not reshuffle between refreshes.
		i, p := i, p
		wg.Add(1)
		go func() {
			defer wg.Done()

			snapshot := ProjectSnapshot{Project: p, FetchedAt: time.Now()}
			if ok, reason := access.Usable(p); !ok {
				snapshot.Skipped, snapshot.SkipReason = true, reason
				out.Snapshots[i] = snapshot
				return
			}

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				snapshot.Skipped, snapshot.SkipReason = true, "cancelled before it started"
				out.Snapshots[i] = snapshot
				return
			}

			// safely for the same reason every other fan-out uses it: a panic
			// in one project's row builder must cost that project, not the
			// process and the terminal with it.
			var result Result
			err := safely(p.Name, func() (err error) {
				result, err = lister.List(ctx, cfg, p, access.Options(p))
				return err
			})
			StampKind(&result, lister.Kind().ID)

			snapshot.Result, snapshot.Err = result, err
			snapshot.FetchedAt = time.Now()
			out.Snapshots[i] = snapshot
		}()
	}
	wg.Wait()

	return out
}

// SortFleetResources orders rows by project then name, so the same estate
// renders the same way every time.
func SortFleetResources(resources []Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		if resources[i].Project != resources[j].Project {
			return resources[i].Project < resources[j].Project
		}
		return resources[i].Name < resources[j].Name
	})
}
