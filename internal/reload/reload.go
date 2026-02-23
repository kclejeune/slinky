// Package reload provides a per-field config reload dispatcher.
// Instead of a monolithic if/else chain in the daemon's config watcher
// callback, each config field change is mapped to a classified action:
// Warn (log only), Callback (custom handler), or Kill (clean shutdown).
package reload

import (
	"log/slog"

	"github.com/kclejeune/slinky/internal/config"
)

// ActionKind classifies how a matching rule is handled.
type ActionKind int

const (
	// Warn logs the rule name but takes no action.
	Warn ActionKind = iota
	// Callback invokes the rule's Handle function.
	Callback
	// Kill invokes the dispatcher's kill function and short-circuits
	// remaining rules.
	Kill
)

// Rule describes a single config-change reaction.
type Rule struct {
	Name   string
	Kind   ActionKind
	Match  func(diff *config.DiffResult) bool
	Handle func(old, new *config.Config, diff *config.DiffResult) // Callback only
}

// Dispatcher evaluates registered rules against config diffs.
type Dispatcher struct {
	prologue []func(old, new *config.Config) // unconditional pre-rule hooks
	rules    []Rule                          // evaluated in registration order
	kill     func()                          // cancels daemon root context
}

// New creates a Dispatcher. The kill function is called when a Kill rule
// matches, and should cancel the daemon's root context for a clean shutdown.
func New(kill func()) *Dispatcher {
	return &Dispatcher{kill: kill}
}

// OnAlways registers an unconditional prologue hook that runs before any
// rules are evaluated. Prologues execute in registration order.
func (d *Dispatcher) OnAlways(fn func(old, new *config.Config)) {
	d.prologue = append(d.prologue, fn)
}

// Register appends a rule. Rules are evaluated in registration order.
func (d *Dispatcher) Register(rule Rule) {
	d.rules = append(d.rules, rule)
}

// Dispatch runs all prologues, then evaluates rules in order against the
// diff. This signature matches the ConfigWatcher callback exactly.
func (d *Dispatcher) Dispatch(old, new *config.Config, diff *config.DiffResult) {
	for _, fn := range d.prologue {
		fn(old, new)
	}

	for _, r := range d.rules {
		if r.Match != nil && !r.Match(diff) {
			continue
		}

		switch r.Kind {
		case Warn:
			slog.Info("config change requires attention", "rule", r.Name)
		case Callback:
			if r.Handle != nil {
				r.Handle(old, new, diff)
			}
		case Kill:
			slog.Info("config change requires restart", "rule", r.Name)
			if d.kill != nil {
				d.kill()
			}
			return
		}
	}
}
