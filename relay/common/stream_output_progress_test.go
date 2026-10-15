package common

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamOutputProgressConcurrentAndAttemptIsolation(t *testing.T) {
	progress := &StreamOutputProgress{}
	require.True(t, progress.FirstUsableOutput().IsZero())
	progress.MarkUsableOutput()
	first := progress.FirstUsableOutput()
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			progress.MarkUsableOutput()
			_ = progress.FirstUsableOutput()
		}()
	}
	wg.Wait()
	require.Equal(t, first, progress.FirstUsableOutput())
	info := &RelayInfo{StreamOutput: progress}
	clone := info.CloneForHedgeAttempt(HedgeRolePrimary)
	require.Nil(t, clone.StreamOutput)
	clone.StreamOutput = &StreamOutputProgress{}
	require.True(t, clone.StreamOutput.FirstUsableOutput().IsZero())
	require.Equal(t, first, info.StreamOutput.FirstUsableOutput())
}
