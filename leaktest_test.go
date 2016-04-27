package leaktest

import (
	"testing"
	"time"
)

type testReporter struct {
	failed bool
}

func (tr *testReporter) Errorf(format string, args ...interface{}) {
	tr.failed = true
}

func TestCheck(t *testing.T) {
	checker := &testReporter{}

	snapshot := Check(checker)
	go func() {
		for {
			time.Sleep(time.Second)
		}
	}()

	snapshot()
	if !checker.failed {
		t.Errorf("didn't catch sleeping goroutine")
	}
}
