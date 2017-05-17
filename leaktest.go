// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package leaktest provides tools to detect leaked goroutines in tests.
// To use it, call "defer util.Check(t)()" at the beginning of each
// test that may use goroutines.
// copied out of the cockroachdb source tree with slight modifications to be
// more re-useable
package leaktest

import (
	"context"
	"runtime"
	"sort"
	"strings"
	"time"
)

// interestingGoroutines returns all goroutines we care about for the purpose
// of leak checking. It excludes testing or runtime ones.
func interestingGoroutines() (gs []string) {
	buf := make([]byte, 2<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		sl := strings.SplitN(g, "\n", 2)
		if len(sl) != 2 {
			continue
		}
		stack := strings.TrimSpace(sl[1])
		if strings.HasPrefix(stack, "testing.RunTests") {
			continue
		}

		if stack == "" ||
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
			continue
		}
		gs = append(gs, strings.TrimSpace(g))
	}
	sort.Strings(gs)
	return
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
	return CheckTimeout(t, 5*time.Second)
}

// CheckTimeout is the same as Check, but with a configurable timeout
func CheckTimeout(t ErrorReporter, dur time.Duration) func() {
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	fn := CheckContext(ctx, t)
	return func() {
		fn()
		cancel()
	}
}

// CheckContext is the same as Check, but uses a context.Context for
// cancellation and timeout control
func CheckContext(ctx context.Context, t ErrorReporter) func() {
	orig := map[string]bool{}
	for _, g := range interestingGoroutines() {
		orig[g] = true
	}
	return func() {
		var leaked []string
		for {
			select {
			case <-ctx.Done():
				t.Errorf("leaktest: timed out checking goroutines")
			default:
				leaked = make([]string, 0)
				for _, g := range interestingGoroutines() {
					if !orig[g] {
						leaked = append(leaked, g)
					}
				}
				if len(leaked) == 0 {
					return
				}
				// don't spin needlessly
				time.Sleep(time.Millisecond * 50)
				continue
			}
			break
		}
		for _, g := range leaked {
			t.Errorf("leaktest: leaked goroutine: %v", g)
		}
	}
}
