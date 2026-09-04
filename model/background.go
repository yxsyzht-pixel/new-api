package model

import (
	"sync"

	"github.com/bytedance/gopkg/util/gopool"
)

// inFlightBackground counts the fire-and-forget goroutines started by
// backgroundGo. Production never waits on them — that is the point of firing
// them — but the suite has to, because they read process globals (RedisEnabled,
// RDB) that a test's cleanup restores. A goroutine still running when the next
// test swaps those globals is a data race, and it made `go test -race ./model`
// fail five runs in six, which costs the package the ability to detect any
// other race at all.
var inFlightBackground sync.WaitGroup

// backgroundGo runs fn detached, exactly as gopool.Go does, while keeping it
// countable. Use it for work whose result nobody awaits but which touches
// shared state.
func backgroundGo(fn func()) {
	inFlightBackground.Add(1)
	gopool.Go(func() {
		defer inFlightBackground.Done()
		fn()
	})
}

// WaitForBackgroundWork blocks until every goroutine started by backgroundGo
// has finished. It exists for tests that mutate the globals such work reads.
func WaitForBackgroundWork() {
	inFlightBackground.Wait()
}
