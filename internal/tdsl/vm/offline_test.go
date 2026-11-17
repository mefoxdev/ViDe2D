package vm

import (
	"math"
	"testing"
)

func TestRunOfflineScalesPerSecondInUpdateOnly(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "var s = 0\nwhen update\n  move right by 60 per second\nwhen every 1 seconds\n  s += 10 per second\n", h)
	e.BeginFrame()
	in.RunPhase(PhaseInit)
	if errs := in.RunOffline(OfflineTick{Steps: 120, CanWrite: func(ObjID) bool { return true }}); len(errs) != 0 {
		t.Fatal(errs[0])
	}
	if math.Abs(h.x()-120) > 1e-9 { // 2 s at 60 px/s in one run
		t.Fatalf("x = %v, want 120", h.x())
	}
	if v, _ := in.Global("s"); math.Abs(v.N-2*10.0/60) > 1e-9 { // two periods, "per second" not scaled in a timer
		t.Fatalf("s = %v", v.N)
	}
	if e.off != nil {
		t.Fatal("offline mode must end with the call")
	}
}

func TestRunOfflineTimersKeepPhaseAndAreCapped(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "var n = 0\nwhen every 1 seconds\n  n += 1\n", h)
	e.BeginFrame()
	in.RunPhase(PhaseInit)
	in.RunOffline(OfflineTick{Steps: 30}) // half a period: nothing yet
	if v, _ := in.Global("n"); v.N != 0 {
		t.Fatalf("n = %v", v.N)
	}
	in.RunOffline(OfflineTick{Steps: 30}) // the period completes
	if v, _ := in.Global("n"); v.N != 1 {
		t.Fatalf("n = %v", v.N)
	}
	in.RunOffline(OfflineTick{Steps: 60 * 100}) // a long absence: at most MaxMissedPeriods runs
	if v, _ := in.Global("n"); v.N != 1+MaxMissedPeriods {
		t.Fatalf("n = %v", v.N)
	}
}

func TestRunOfflineRefusesWritingOtherObjectsVariables(t *testing.T) {
	h := newHost(60)
	e := NewEngine(h)
	other := e.NewInstance(compile(t, "var v = 1\n"), 2)
	other.Initialize()
	src := "var o = nothing\nwhen every 1 seconds\n  o.v = 5\n"
	in2 := e.NewInstance(compile(t, src), 3)
	e.BeginFrame()
	in2.RunPhase(PhaseInit)
	in2.globals[0] = Obj(2)
	errs := in2.RunOffline(OfflineTick{Steps: 60, CanWrite: func(id ObjID) bool { return id == 3 }})
	if len(errs) != 1 || errs[0].Code != CodeOffline {
		t.Fatalf("expected TD406, got %v", errs)
	}
	if v, _ := other.Global("v"); v.N != 1 {
		t.Fatal("the other object was written")
	}
	errs = in2.RunOffline(OfflineTick{Steps: 60, CanWrite: func(id ObjID) bool { return true }})
	if len(errs) != 0 {
		t.Fatal(errs[0])
	}
	if v, _ := other.Global("v"); v.N != 5 {
		t.Fatal("a permitted write did not happen")
	}
}

func waiter(t *testing.T, e *Engine, p *Program, id ObjID) *Instance {
	in := e.NewInstance(p, id)
	e.BeginFrame()
	for _, ph := range []Phase{PhaseInit, PhaseStart} {
		in.RunPhase(ph)
	}
	if in.Waiting() != 1 {
		t.Fatal("the handler must be waiting")
	}
	return in
}

func TestSnapshotOfWaitingRunsIsBounded(t *testing.T) {
	h := newHost(60)
	e := NewEngine(h)
	e.Limits.SnapshotTasks = 2
	p := compile(t, "when start\n  wait 5 seconds\n")
	var states []*InstanceState
	dropped := 0
	for id := ObjID(1); id <= 4; id++ {
		st := waiter(t, e, p, id).Snapshot()
		states = append(states, st)
		dropped += st.Dropped
	}
	if dropped != 2 || e.snapTasks != 2 {
		t.Fatalf("dropped %d, held %d: want 2 and 2", dropped, e.snapTasks)
	}
	states[0].Release()
	states[0].Release() // twice is harmless
	if e.snapTasks != 1 {
		t.Fatalf("held %d after a release", e.snapTasks)
	}
	// restoring gives the share back, too
	in := waiter(t, e, p, 9)
	if !in.Restore(states[1]) || e.snapTasks != 0 {
		t.Fatalf("held %d after a restore", e.snapTasks)
	}
	// a snapshot with a huge list in a waiting run does not fit the item budget
	e.Limits.SnapshotItems = 100
	in2 := e.NewInstance(compile(t, "when start\n  var big = []\n  repeat 500 times\n    add 1 to big\n  wait 5 seconds\n"), 10)
	e.BeginFrame()
	in2.RunPhase(PhaseInit)
	in2.RunPhase(PhaseStart)
	if st := in2.Snapshot(); st.Dropped != 1 {
		t.Fatalf("the run with 500 items must be dropped, dropped=%d", st.Dropped)
	}
}
