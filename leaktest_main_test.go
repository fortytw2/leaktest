package leaktest_test

import (
	"testing"

	"github.com/fortytw2/leaktest"
)

func TestMain(m *testing.M) {
	leaktest.CheckMain(m)
}

func TestSilly(t *testing.T) {
	go func() {
		select {}
	}()
}
