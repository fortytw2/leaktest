// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package leaktest provides tools to detect leaked goroutines in tests.
// To use it, call "defer leaktest.Check(t)()" at the beginning of each
// test that may use goroutines.
// copied out of the cockroachdb source tree with slight modifications to be
// more re-useable
package leaktest

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TickerInterval defines the interval used by the ticker in Check* functions.
var TickerInterval = time.Millisecond * 50

type goroutine struct {
	id    uint64
	stack string
}

func (gr *goroutine) equal(other *goroutine) bool {
	if gr == nil {
		return other == nil
	}

	if other == nil {
		return false
	}

	return gr.id == other.id && gr.stack == other.stack
}

type goroutineByID []*goroutine

func (g goroutineByID) Len() int           { return len(g) }
func (g goroutineByID) Less(i, j int) bool { return g[i].id < g[j].id }
func (g goroutineByID) Swap(i, j int)      { g[i], g[j] = g[j], g[i] }

type LeakCheckConfiguration struct {
	RoutinesSafeToIgnore []string
}

var (
	DefaultCheckConfiguration = LeakCheckConfiguration{
		RoutinesSafeToIgnore: []string{
			// Ignore HTTP keep alives
			").readLoop(",
			").writeLoop(",

			// Below are the stacks ignored by the upstream leaktest code.
			"testing.Main(",
			"testing.(*T).Run(",
			"runtime.goexit",
			"created by runtime.gc",
			"interestingGoroutines",
			"runtime.MHeap_Scavenger",
			"signal.signal_recv",
			"sigterm.handler",
			"runtime_mcall",
			"goroutine in C code",
		},
	}
)

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}

	return false
}

func (lcc LeakCheckConfiguration) interestingGoroutine(g string) (*goroutine, error) {
	sl := strings.SplitN(g, "\n", 2)
	if len(sl) != 2 {
		return nil, fmt.Errorf("error parsing stack: %q", g)
	}
	stack := strings.TrimSpace(sl[1])
	if strings.HasPrefix(stack, "testing.RunTests") {
		return nil, nil
	}

	if stack == "" || containsAny(stack, lcc.RoutinesSafeToIgnore) {
		return nil, nil
	}

	// Parse the goroutine's ID from the header line.
	h := strings.SplitN(sl[0], " ", 3)
	if len(h) < 3 {
		return nil, fmt.Errorf("error parsing stack header: %q", sl[0])
	}
	id, err := strconv.ParseUint(h[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("error parsing goroutine id: %s", err)
	}

	return &goroutine{id: id, stack: strings.TrimSpace(g)}, nil
}

// interestingGoroutines returns all goroutines we care about for the purpose
// of leak checking. It excludes testing or runtime ones.
func (lcc LeakCheckConfiguration) interestingGoroutines(t ErrorReporter) []*goroutine {
	buf := make([]byte, 2<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var gs []*goroutine
	for _, g := range strings.Split(string(buf), "\n\n") {
		gr, err := lcc.interestingGoroutine(g)
		if err != nil {
			t.Errorf("leaktest: %s", err)
			continue
		} else if gr == nil {
			continue
		}
		gs = append(gs, gr)
	}
	sort.Sort(goroutineByID(gs))
	return gs
}

// leakedGoroutines returns all goroutines we are considering leaked and
// the boolean flag indicating if no leaks detected
func leakedGoroutines(orig map[uint64]bool, interesting []*goroutine) ([]string, bool) {
	leaked := make([]string, 0)
	flag := true
	for _, g := range interesting {
		if !orig[g.id] {
			leaked = append(leaked, g.stack)
			flag = false
		}
	}
	return leaked, flag
}

func (lcc LeakCheckConfiguration) Check(t ErrorReporter) func() {
	return lcc.CheckTimeout(t, 5*time.Second)
}

func (lcc LeakCheckConfiguration) CheckTimeout(t ErrorReporter, dur time.Duration) func() {
	ctx, cancel := context.WithCancel(context.Background())
	fn := lcc.CheckContext(ctx, t)
	return func() {
		timer := time.AfterFunc(dur, cancel)
		fn()
		// Remember to clean up the timer and context
		timer.Stop()
		cancel()
	}
}

func (lcc LeakCheckConfiguration) CheckContext(ctx context.Context, t ErrorReporter) func() {
	orig := map[uint64]bool{}
	for _, g := range lcc.interestingGoroutines(t) {
		orig[g.id] = true
	}
	return func() {
		var leaked []string
		var ok bool
		// fast check if we have no leaks
		if leaked, ok = leakedGoroutines(orig, lcc.interestingGoroutines(t)); ok {
			return
		}
		ticker := time.NewTicker(TickerInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if leaked, ok = leakedGoroutines(orig, lcc.interestingGoroutines(t)); ok {
					return
				}
				continue
			case <-ctx.Done():
				t.Errorf("leaktest: %v", ctx.Err())
			}
			break
		}

		for _, g := range leaked {
			t.Errorf("leaktest: leaked goroutine: %v", g)
		}
	}
}

// ErrorReporter is a tiny subset of a testing.TB to make testing not such a
// massive pain
type ErrorReporter interface {
	Errorf(format string, args ...interface{})
}

// Check snapshots the currently-running goroutines and returns a
// function to be run at the end of tests to see whether any
// goroutines leaked, waiting up to 5 seconds in error conditions
func Check(t ErrorReporter) func() {
	return DefaultCheckConfiguration.Check(t)
}

// CheckTimeout is the same as Check, but with a configurable timeout
func CheckTimeout(t ErrorReporter, dur time.Duration) func() {
	return DefaultCheckConfiguration.CheckTimeout(t, dur)
}

// CheckContext is the same as Check, but uses a context.Context for
// cancellation and timeout control
func CheckContext(ctx context.Context, t ErrorReporter) func() {
	return DefaultCheckConfiguration.CheckContext(ctx, t)
}
