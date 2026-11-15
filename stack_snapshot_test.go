package leaktest

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func waitForStackDump(depth int, ready, done *sync.WaitGroup, release <-chan struct{}) {
	if depth == 0 {
		ready.Done()
		<-release
		done.Done()
		return
	}
	waitForStackDump(depth-1, ready, done, release)
	runtime.KeepAlive(depth)
}

func waitForNewLeak(ready, done *sync.WaitGroup, release <-chan struct{}) {
	ready.Done()
	<-release
	done.Done()
}

func TestLargeStackSnapshot(t *testing.T) {
	const count = 500
	var ready, done sync.WaitGroup
	ready.Add(count)
	done.Add(count)
	release := make(chan struct{})
	defer func() { close(release); done.Wait() }()
	for i := 0; i < count; i++ {
		go waitForStackDump(64, &ready, &done, release)
	}
	ready.Wait()

	// Confirm that real runtime output exceeds the old fixed-size buffer.
	dump := make([]byte, 8<<20)
	n := runtime.Stack(dump, true)
	for n == len(dump) {
		dump = make([]byte, 2*len(dump))
		n = runtime.Stack(dump, true)
	}
	if n <= 2<<20 {
		t.Fatalf("unexpected fixture stack size: %d", n)
	}
	reporter := &testReporter{}
	goroutines := interestingGoroutines(reporter)
	if reporter.failed {
		t.Fatalf("snapshot reported a parse error: %s", reporter.msg)
	}
	found := 0
	for _, g := range goroutines {
		if strings.Contains(g.stack, "waitForStackDump(") {
			found++
		}
	}
	if found != count {
		t.Fatalf("snapshot found %d waiting goroutines; want %d", found, count)
	}

	// The public checker must continue to ignore all preexisting goroutines.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	check := CheckContext(ctx, reporter)
	check()
	if reporter.failed {
		t.Fatalf("preexisting goroutines reported as leaks: %s", reporter.msg)
	}

	// A new goroutine must still be reported, even behind the large snapshot.
	check = CheckContext(ctx, reporter)
	ready.Add(1)
	done.Add(1)
	go waitForNewLeak(&ready, &done, release)
	ready.Wait()
	cancel()
	check()
	if !reporter.failed || !strings.Contains(reporter.msg, "waitForNewLeak(") {
		t.Fatalf("new goroutine was not reported: %s", reporter.msg)
	}
}
