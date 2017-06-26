package leaktest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type testReporter struct {
	failed bool
	msg    string
}

func (tr *testReporter) Errorf(format string, args ...interface{}) {
	tr.failed = true
	tr.msg = fmt.Sprintf(format, args)
}

var leakyFuncs = []func(){
	// Infinite for loop
	func() {
		for {
			time.Sleep(time.Second)
		}
	},
	// Select on a channel not referenced by other goroutines.
	func() {
		c := make(chan struct{}, 0)
		select {
		case <-c:
		}
	},
	// Blocked select on channels not referenced by other goroutines.
	func() {
		c := make(chan struct{}, 0)
		c2 := make(chan struct{}, 0)
		select {
		case <-c:
		case c2 <- struct{}{}:
		}
	},
	// Blocking wait on sync.Mutex that isn't referenced by other goroutines.
	func() {
		var mu sync.Mutex
		mu.Lock()
		mu.Lock()
	},
	// Blocking wait on sync.RWMutex that isn't referenced by other goroutines.
	func() {
		var mu sync.RWMutex
		mu.RLock()
		mu.Lock()
	},
	func() {
		var mu sync.Mutex
		mu.Lock()
		c := sync.NewCond(&mu)
		c.Wait()
	},
}

func TestCheck(t *testing.T) {
	// this works because the running goroutine is left running at the
	// start of the next test case - so the previous leaks don't affect the
	// check for the next one
	for i, fn := range leakyFuncs {
		checker := &testReporter{}
		snapshot := CheckTimeout(checker, time.Second)
		go fn()

		snapshot()
		if !checker.failed {
			t.Errorf("didn't catch sleeping goroutine, test #%d", i)
		}
	}
}

func TestEmptyLeak(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer CheckContext(ctx, t)()
	time.Sleep(time.Second)
}

// TestChangingStackTrace validates that a change in a preexisting goroutine's
// stack is not detected as a leaked goroutine.
func TestChangingStackTrace(t *testing.T) {
	started := make(chan struct{})
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	defer close(c2)
	go func() {
		close(started)
		<-c1
		<-c2
	}()
	<-started
	func() {
		defer CheckTimeout(t, time.Second)()
		close(c1)
	}()
}

func TestInterestingGoroutine(t *testing.T) {
	s := "goroutine 123 [running]:\nmain.main()"
	gr, ok := interestingGoroutine(s)
	if !ok {
		t.Error("should be ok")
	}
	if gr.id != 123 {
		t.Errorf("goroutine id = %d; want %d", gr.id, 123)
	}
	if gr.stack != s {
		t.Errorf("goroutine stack = %q; want %q", gr.stack, s)
	}

	stacks := []string{
		"goroutine 123 [running]:",
		"goroutine 123 [running]:\ntesting.RunTests",
		"goroutine 856105:\nmain.main()",
		"goroutine NaN [running]:\nmain.main()",
	}
	for _, s := range stacks {
		_, ok := interestingGoroutine(s)
		if ok {
			t.Errorf("should not be ok: %q", s)
		}
	}
}
