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

type goroutineByID []*goroutine

func (g goroutineByID) Len() int           { return len(g) }
func (g goroutineByID) Less(i, j int) bool { return g[i].id < g[j].id }
func (g goroutineByID) Swap(i, j int)      { g[i], g[j] = g[j], g[i] }

func interestingGoroutine(g string, opts ...SkipGoroutineOption) (*goroutine, error) {
	sl := strings.SplitN(g, "\n", 2)
	if len(sl) != 2 {
		return nil, fmt.Errorf("error parsing stack: %q", g)
	}
	stack := strings.TrimSpace(sl[1])
	if strings.HasPrefix(stack, "testing.RunTests") {
		return nil, nil
	}

	if stack == "" ||
		// Ignore HTTP keep alives
		strings.Contains(stack, ").readLoop(") ||
		strings.Contains(stack, ").writeLoop(") ||
		// Below are the stacks ignored by the upstream leaktest code.
		strings.Contains(stack, "testing.Main(") ||
		strings.Contains(stack, "testing.(*T).Run(") ||
		strings.Contains(stack, "runtime.goexit") ||
		strings.Contains(stack, "created by runtime.gc") ||
		strings.Contains(stack, "interestingGoroutines") ||
		strings.Contains(stack, "runtime.MHeap_Scavenger") ||
		strings.Contains(stack, "signal.signal_recv") ||
		strings.Contains(stack, "sigterm.handler") ||
		strings.Contains(stack, "runtime_mcall") ||
		strings.Contains(stack, "goroutine in C code") {
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

	stack = strings.TrimSpace(g)
	for _, opt := range opts {
		if opt(stack) {
			return nil, nil
		}
	}

	return &goroutine{id: id, stack: stack}, nil
}

// interestingGoroutines returns all goroutines we care about for the purpose
// of leak checking. It excludes testing or runtime ones.
func interestingGoroutines(t ErrorReporter, opts ...SkipGoroutineOption) []*goroutine {
	buf := make([]byte, 2<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var gs []*goroutine
	for _, g := range strings.Split(string(buf), "\n\n") {
		gr, err := interestingGoroutine(g, opts...)
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

// ErrorReporter is a tiny subset of a testing.TB to make testing not such a
// massive pain
type ErrorReporter interface {
	Errorf(format string, args ...interface{})
}

// SkipGoroutineOption is a function that can be passed to check functions
// to skip some leaked goroutines based on the content of their stack.
type SkipGoroutineOption func(stack string) (skip bool)

// Check snapshots the currently-running goroutines and returns a
// function to be run at the end of tests to see whether any
// goroutines leaked, waiting up to 5 seconds in error conditions
func Check(t ErrorReporter, opts ...SkipGoroutineOption) func() {
	return CheckTimeout(t, 5*time.Second, opts...)
}

// CheckTimeout is the same as Check, but with a configurable timeout
func CheckTimeout(t ErrorReporter, dur time.Duration, opts ...SkipGoroutineOption) func() {
	ctx, cancel := context.WithCancel(context.Background())
	fn := CheckContext(ctx, t, opts...)
	return func() {
		timer := time.AfterFunc(dur, cancel)
		fn()
		// Remember to clean up the timer and context
		timer.Stop()
		cancel()
	}
}

// CheckContext is the same as Check, but uses a context.Context for
// cancellation and timeout control
func CheckContext(ctx context.Context, t ErrorReporter, opts ...SkipGoroutineOption) func() {
	orig := map[uint64]bool{}
	for _, g := range interestingGoroutines(t, opts...) {
		orig[g.id] = true
	}
	return func() {
		var leaked []string
		var ok bool
		// fast check if we have no leaks
		if leaked, ok = leakedGoroutines(orig, interestingGoroutines(t, opts...)); ok {
			return
		}
		ticker := time.NewTicker(TickerInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if leaked, ok = leakedGoroutines(orig, interestingGoroutines(t, opts...)); ok {
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
