package gcp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// fakeLister returns whatever the test tells it to, per project.
type fakeLister struct {
	kind    Kind
	results map[string]Result
	errs    map[string]error
	// inFlight tracks concurrency so the bound can be asserted.
	inFlight, peak int32
	delay          time.Duration
}

func (f *fakeLister) Kind() Kind { return f.kind }

func (f *fakeLister) List(ctx context.Context, _ *config.Config, p config.Project, _ []option.ClientOption) (Result, error) {
	n := atomic.AddInt32(&f.inFlight, 1)
	for {
		peak := atomic.LoadInt32(&f.peak)
		if n <= peak || atomic.CompareAndSwapInt32(&f.peak, peak, n) {
			break
		}
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	atomic.AddInt32(&f.inFlight, -1)

	if err, ok := f.errs[p.Name]; ok {
		return Result{}, err
	}
	return f.results[p.Name], nil
}

// allUsable lets every project through.
type allUsable struct{}

func (allUsable) Usable(config.Project) (bool, string)         { return true, "" }
func (allUsable) Options(config.Project) []option.ClientOption { return nil }

// gatedAccess refuses the projects it is told to.
type gatedAccess struct{ refuse map[string]string }

func (g gatedAccess) Usable(p config.Project) (bool, string) {
	if reason, blocked := g.refuse[p.Name]; blocked {
		return false, reason
	}
	return true, ""
}
func (g gatedAccess) Options(config.Project) []option.ClientOption { return nil }

func fleetProjects(names ...string) []config.Project {
	out := make([]config.Project, 0, len(names))
	for _, n := range names {
		out = append(out, config.Project{Name: n, ProjectID: n + "-1234"})
	}
	return out
}

func row(name string) Resource {
	return Resource{Name: name, Location: "us-central1-a", Status: "RUNNING", Row: []string{name}}
}

// Every project produces a snapshot, including the ones that could not be
// read. A sweep that silently dropped them would hand back a table that looks
// like the estate and is actually the subset that answered — the single most
// dangerous thing a cross-project view can do.
func TestSweepAccountsForEveryProjectIncludingTheOnesItCouldNotRead(t *testing.T) {
	projects := fleetProjects("dev", "uat", "prod", "locked", "loggedout")
	lister := &fakeLister{
		kind: Kind{ID: "vm", Title: "VM Instances"},
		results: map[string]Result{
			"dev":  {Resources: []Resource{row("api-1")}},
			"uat":  {Resources: []Resource{row("api-1")}},
			"prod": {Resources: []Resource{row("api-1"), row("api-2")}},
		},
		errs: map[string]error{"locked": errors.New("permission denied")},
	}
	access := gatedAccess{refuse: map[string]string{"loggedout": "not logged in"}}

	fleet := SweepFleet(context.Background(), nil, lister, projects, access)

	if len(fleet.Snapshots) != len(projects) {
		t.Fatalf("got %d snapshots, want one per project (%d)", len(fleet.Snapshots), len(projects))
	}
	complete, partial, failed, skipped := fleet.Counts()
	if complete != 3 || partial != 0 || failed != 1 || skipped != 1 {
		t.Errorf("counts = %d complete, %d partial, %d failed, %d skipped; want 3/0/1/1",
			complete, partial, failed, skipped)
	}

	// The skipped project has to say why, or it reads as a project with
	// nothing in it.
	for _, s := range fleet.Snapshots {
		if s.Project.Name == "loggedout" {
			if !s.Skipped || s.SkipReason == "" {
				t.Errorf("skipped project carries no reason: %+v", s)
			}
		}
	}
}

// The count is the number people will quote. It must not look like an answer
// about the estate when two projects never answered.
func TestFleetIsNotTrustworthyWhenAnyProjectIsMissing(t *testing.T) {
	projects := fleetProjects("dev", "prod")
	lister := &fakeLister{
		kind:    Kind{ID: "vm"},
		results: map[string]Result{"dev": {Resources: []Resource{row("a")}}, "prod": {Resources: []Resource{row("b")}}},
	}

	whole := SweepFleet(context.Background(), nil, lister, projects, allUsable{})
	if !whole.Trustworthy() {
		t.Error("a sweep where every project answered completely reports as untrustworthy")
	}

	// One partial project is enough to spoil the total.
	lister.results["prod"] = Result{
		Resources: []Resource{row("b")},
		Warnings:  []Warning{scopeWarning("us-east4", ReasonDenied, "permission denied")},
	}
	partial := SweepFleet(context.Background(), nil, lister, projects, allUsable{})
	if partial.Trustworthy() {
		t.Error("a sweep with a partial project reports as trustworthy")
	}

	// So is one skipped project.
	skipped := SweepFleet(context.Background(), nil, lister, projects,
		gatedAccess{refuse: map[string]string{"prod": "not logged in"}})
	if skipped.Trustworthy() {
		t.Error("a sweep that never asked one project reports as trustworthy")
	}
}

// A fleet fetch multiplies out as projects × the regions each kind sweeps.
// Unbounded, ten projects against a six-region kind is sixty concurrent calls
// — enough to hit a per-minute quota on a service nobody was having trouble
// with, and enough to make a proxy the bottleneck for the whole machine.
func TestSweepBoundsConcurrency(t *testing.T) {
	projects := fleetProjects("p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10")
	lister := &fakeLister{
		kind:    Kind{ID: "vm"},
		results: map[string]Result{},
		delay:   20 * time.Millisecond,
	}

	SweepFleet(context.Background(), nil, lister, projects, allUsable{})

	if peak := atomic.LoadInt32(&lister.peak); peak > fleetConcurrency {
		t.Errorf("peak concurrency was %d, above the %d bound", peak, fleetConcurrency)
	}
}

// Leaving the screen has to stop costing API calls, or a fleet view is
// something you regret opening.
func TestCancellingTheSweepStopsProjectsThatHaveNotStarted(t *testing.T) {
	projects := fleetProjects("p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8")
	lister := &fakeLister{
		kind:    Kind{ID: "vm"},
		results: map[string]Result{},
		delay:   50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	var fleet FleetResult
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		fleet = SweepFleet(ctx, nil, lister, projects, allUsable{})
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	wg.Wait()

	_, _, _, skipped := fleet.Counts()
	if skipped == 0 {
		t.Error("cancelling the sweep started every project anyway")
	}
	// And every project is still accounted for, cancelled ones included.
	if len(fleet.Snapshots) != len(projects) {
		t.Errorf("got %d snapshots after cancel, want %d", len(fleet.Snapshots), len(projects))
	}
}

// Two instances called api-1 in dev and prod are the pair a comparison exists
// to look at. Without the project on the row they are indistinguishable.
func TestFlattenedRowsCarryTheirProject(t *testing.T) {
	projects := fleetProjects("dev", "prod")
	lister := &fakeLister{
		kind: Kind{ID: "vm"},
		results: map[string]Result{
			"dev":  {Resources: []Resource{row("api-1")}},
			"prod": {Resources: []Resource{row("api-1")}},
		},
	}

	fleet := SweepFleet(context.Background(), nil, lister, projects, allUsable{})
	rows := fleet.Resources()
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	seen := map[string]bool{}
	for _, r := range rows {
		if r.Project == "" {
			t.Fatalf("row %q carries no project", r.Name)
		}
		seen[r.Project] = true
	}
	if !seen["dev"] || !seen["prod"] {
		t.Errorf("rows came from %v, want both projects", seen)
	}
}

// Snapshot order follows the configured project order, not completion order,
// or the table reshuffles between refreshes for no reason a reader can see.
func TestSnapshotOrderFollowsTheConfigNotTheRace(t *testing.T) {
	projects := fleetProjects("alpha", "bravo", "charlie", "delta")
	lister := &fakeLister{kind: Kind{ID: "vm"}, results: map[string]Result{}, delay: 5 * time.Millisecond}

	for i := 0; i < 5; i++ {
		fleet := SweepFleet(context.Background(), nil, lister, projects, allUsable{})
		for j, s := range fleet.Snapshots {
			if s.Project.Name != projects[j].Name {
				t.Fatalf("run %d: snapshot %d is %q, want %q",
					i, j, s.Project.Name, projects[j].Name)
			}
		}
	}
}

// A panic in one project's row builder must cost that project, not the process
// and the terminal with it.
func TestSweepSurvivesAPanickingProject(t *testing.T) {
	projects := fleetProjects("good", "boom")
	lister := &panicLister{kind: Kind{ID: "vm"}}

	fleet := SweepFleet(context.Background(), nil, lister, projects, allUsable{})

	if len(fleet.Snapshots) != 2 {
		t.Fatalf("got %d snapshots", len(fleet.Snapshots))
	}
	var failed int
	for _, s := range fleet.Snapshots {
		if s.Err != nil {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("%d projects failed, want just the panicking one", failed)
	}
	if len(fleet.Resources()) != 1 {
		t.Errorf("got %d rows, want the healthy project's one", len(fleet.Resources()))
	}
}

type panicLister struct{ kind Kind }

func (p *panicLister) Kind() Kind { return p.kind }
func (p *panicLister) List(_ context.Context, _ *config.Config, proj config.Project, _ []option.ClientOption) (Result, error) {
	if proj.Name == "boom" {
		var nilMap map[string]string
		nilMap["write"] = ""
	}
	return Result{Resources: []Resource{row("ok")}}, nil
}
