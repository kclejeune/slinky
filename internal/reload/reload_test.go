package reload

import (
	"testing"

	"github.com/kclejeune/slinky/internal/config"
)

func TestPrologueOrder(t *testing.T) {
	d := New(nil)

	var order []int
	d.OnAlways(func(_, _ *config.Config) { order = append(order, 1) })
	d.OnAlways(func(_, _ *config.Config) { order = append(order, 2) })
	d.OnAlways(func(_, _ *config.Config) { order = append(order, 3) })

	d.Dispatch(nil, nil, &config.DiffResult{})

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("prologue order = %v, want [1 2 3]", order)
	}
}

func TestPrologueBeforeRules(t *testing.T) {
	d := New(nil)

	var order []string
	d.OnAlways(func(_, _ *config.Config) { order = append(order, "prologue") })
	d.Register(Rule{
		Name:  "rule",
		Kind:  Callback,
		Match: func(_ *config.DiffResult) bool { return true },
		Handle: func(_, _ *config.Config, _ *config.DiffResult) {
			order = append(order, "rule")
		},
	})

	d.Dispatch(nil, nil, &config.DiffResult{})

	if len(order) != 2 || order[0] != "prologue" || order[1] != "rule" {
		t.Errorf("order = %v, want [prologue rule]", order)
	}
}

func TestWarnNoSideEffects(t *testing.T) {
	d := New(nil)

	called := false
	d.Register(Rule{
		Name:  "warn-rule",
		Kind:  Warn,
		Match: func(_ *config.DiffResult) bool { return true },
		Handle: func(_, _ *config.Config, _ *config.DiffResult) {
			called = true
		},
	})

	d.Dispatch(nil, nil, &config.DiffResult{})

	if called {
		t.Error("Warn rule should not invoke Handle")
	}
}

func TestCallbackExecuted(t *testing.T) {
	d := New(nil)

	var gotOld, gotNew *config.Config
	var gotDiff *config.DiffResult

	old := config.DefaultConfig()
	new := config.DefaultConfig()
	new.Files["netrc"] = &config.FileConfig{Render: "native", Template: "/tpl"}
	diff := config.Diff(old, new)

	d.Register(Rule{
		Name:  "cb",
		Kind:  Callback,
		Match: func(d *config.DiffResult) bool { return d.FilesChanged() },
		Handle: func(o, n *config.Config, d *config.DiffResult) {
			gotOld = o
			gotNew = n
			gotDiff = d
		},
	})

	d.Dispatch(old, new, diff)

	if gotOld != old || gotNew != new || gotDiff != diff {
		t.Error("Callback did not receive correct arguments")
	}
}

func TestKillInvokesCancel(t *testing.T) {
	killed := false
	d := New(func() { killed = true })

	d.Register(Rule{
		Name:  "kill-rule",
		Kind:  Kill,
		Match: func(_ *config.DiffResult) bool { return true },
	})

	d.Dispatch(nil, nil, &config.DiffResult{})

	if !killed {
		t.Error("Kill rule did not invoke cancel")
	}
}

func TestKillShortCircuits(t *testing.T) {
	d := New(func() {})

	afterKill := false
	d.Register(Rule{
		Name:  "kill-rule",
		Kind:  Kill,
		Match: func(_ *config.DiffResult) bool { return true },
	})
	d.Register(Rule{
		Name:  "after-kill",
		Kind:  Callback,
		Match: func(_ *config.DiffResult) bool { return true },
		Handle: func(_, _ *config.Config, _ *config.DiffResult) {
			afterKill = true
		},
	})

	d.Dispatch(nil, nil, &config.DiffResult{})

	if afterKill {
		t.Error("rules after Kill should not execute")
	}
}

func TestNonMatchingRulesSkipped(t *testing.T) {
	d := New(nil)

	called := false
	d.Register(Rule{
		Name:  "no-match",
		Kind:  Callback,
		Match: func(_ *config.DiffResult) bool { return false },
		Handle: func(_, _ *config.Config, _ *config.DiffResult) {
			called = true
		},
	})

	d.Dispatch(nil, nil, &config.DiffResult{})

	if called {
		t.Error("non-matching rule should not be called")
	}
}

func TestMultipleMatchingRulesFireInOrder(t *testing.T) {
	d := New(nil)

	var order []int
	for i := range 3 {
		d.Register(Rule{
			Name:  "rule",
			Kind:  Callback,
			Match: func(_ *config.DiffResult) bool { return true },
			Handle: func(_, _ *config.Config, _ *config.DiffResult) {
				order = append(order, i)
			},
		})
	}

	d.Dispatch(nil, nil, &config.DiffResult{})

	if len(order) != 3 || order[0] != 0 || order[1] != 1 || order[2] != 2 {
		t.Errorf("order = %v, want [0 1 2]", order)
	}
}

func TestNilMatchAlwaysFires(t *testing.T) {
	d := New(nil)

	called := false
	d.Register(Rule{
		Name:  "nil-match",
		Kind:  Callback,
		Match: nil,
		Handle: func(_, _ *config.Config, _ *config.DiffResult) {
			called = true
		},
	})

	d.Dispatch(nil, nil, &config.DiffResult{})

	if !called {
		t.Error("rule with nil Match should always fire")
	}
}
