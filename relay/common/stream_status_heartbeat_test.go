package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The heartbeat count rides in the line every stream already logs, so two days
// of production tells us who needed the beat and whether they still got cut.
func TestTheSummarySaysHowManyBeatsWentOut(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonClientGone, nil)

	assert.NotContains(t, s.Summary(), "heartbeats",
		"没发过心跳的流不该多出这个字段")

	for i := 0; i < 3; i++ {
		s.Beat()
	}
	assert.Equal(t, int64(3), s.Heartbeats())
	assert.Contains(t, s.Summary(), "heartbeats=3")
	assert.True(t, strings.HasPrefix(s.Summary(), "reason="),
		"原有字段的顺序不能被打乱,日志还要能按老方式解析")
}

// A nil status is the quiet case the rest of this file already tolerates.
func TestBeatingNothingIsHarmless(t *testing.T) {
	var s *StreamStatus
	s.Beat()
	assert.Zero(t, s.Heartbeats())
}
