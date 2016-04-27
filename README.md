Leaktest
------

Refactored, tested variant of the goroutine leak detector found in the `cockroachdb`
source tree.

Takes a snapshot of running goroutines at the start of a test, and at the end -
compares the two and viola. Ignores runtime/sys goroutines. Doesn't place nice
with `t.Parallel()` right now, but there are plans to do so

### Example

This test fails, because it leaks a goroutine :o

```go
func TestPool(t *testing.T) {
	defer leaktest.Check(t)()

    go func() {
        for {
            time.Sleep(time.Second)
        }
    }
}
```


LICENSE
------
Header in leaktest.go - Apache something, per the Go Authors
