package logger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withLogDir(t *testing.T, dir string) {
	t.Helper()
	previous := common.LogDir
	value := dir
	common.LogDir = &value
	t.Cleanup(func() { common.LogDir = previous })
}

// A log file that cannot be opened used to reach log.Fatal — inside a goroutine
// a busy hour starts on its own. A full disk or an exhausted fd table therefore
// took the whole gateway down, answering a transient fault with the most
// permanent response available.
func TestRotationSurvivesAnUnopenableLogFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(dir, []byte("x"), 0o644)) // a file where a dir is expected
	withLogDir(t, dir)

	before := GetCurrentLogPath()
	assert.NotPanics(t, SetupLogger, "rotation must not take the process with it")
	assert.Equal(t, before, GetCurrentLogPath(),
		"a failed rotation must leave the working log path alone")
	assert.False(t, setupLogWorking.Load(), "the guard was left set, so rotation can never run again")
}

func TestRotationOpensAndSwitchesTheFile(t *testing.T) {
	dir := t.TempDir()
	withLogDir(t, dir)

	SetupLogger()
	path := GetCurrentLogPath()
	require.NotEmpty(t, path)
	assert.Equal(t, dir, filepath.Dir(path))
	_, err := os.Stat(path)
	assert.NoError(t, err)
	assert.False(t, setupLogWorking.Load())
}

// The guard is read and written from every goroutine that logs. As a plain bool
// it carried no happens-before, so two could both see it unset and both start a
// rotation; the compare-and-swap is what makes the claim exclusive. This drives
// the real predicate rather than the atomic underneath it, so a change to how
// logHelper decides is what the test is actually holding.
func TestOnlyOneGoroutineClaimsTheRotation(t *testing.T) {
	setupLogWorking.Store(false)
	logCount.Store(maxLogCount)
	t.Cleanup(func() { setupLogWorking.Store(false); logCount.Store(0) })

	// Repeated rounds, because a read-then-write loses only on an interleaving
	// and a single burst can miss it. One double claim anywhere is the failure.
	doubles := 0
	for round := 0; round < 300; round++ {
		setupLogWorking.Store(false)
		logCount.Store(maxLogCount)

		var mu sync.Mutex
		claims := 0
		var wg sync.WaitGroup
		for i := 0; i < 64; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if claimRotation() {
					mu.Lock()
					claims++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if claims != 1 {
			doubles++
		}
	}
	assert.Zero(t, doubles, "%d of 300 rounds did not claim the rotation exactly once", doubles)
}

// Below the threshold nobody rotates, or a quiet process would churn its log
// file on every line.
func TestNobodyClaimsBelowTheThreshold(t *testing.T) {
	setupLogWorking.Store(false)
	logCount.Store(0)
	t.Cleanup(func() { setupLogWorking.Store(false); logCount.Store(0) })

	for i := 0; i < 100; i++ {
		require.False(t, claimRotation(), "rotated at count %d, far below maxLogCount", i)
	}
}

// The claim has to be released when the rotation finishes, or the process
// rotates once and never again however large the log grows.
func TestTheClaimIsReleasedSoRotationCanRecur(t *testing.T) {
	dir := t.TempDir()
	withLogDir(t, dir)
	setupLogWorking.Store(false)
	logCount.Store(maxLogCount)
	t.Cleanup(func() { setupLogWorking.Store(false); logCount.Store(0) })

	require.True(t, claimRotation(), "the first rotation was not claimed")
	SetupLogger()
	require.False(t, setupLogWorking.Load(), "SetupLogger left the claim held")

	logCount.Store(maxLogCount)
	assert.True(t, claimRotation(), "the claim was never released, so the log can never rotate again")
}
