package common

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ebmlElement writes one element with an explicit size, the layout produced by
// encoders that know the length up front.
func ebmlElement(id []byte, body []byte) []byte {
	out := append([]byte{}, id...)
	out = append(out, ebmlEncodeSize(int64(len(body)))...)
	return append(out, body...)
}

// ebmlUnknownSizeElement writes an element with the "size unknown" marker, the
// layout browser MediaRecorder produces for a live Segment.
func ebmlUnknownSizeElement(id []byte, body []byte) []byte {
	out := append([]byte{}, id...)
	out = append(out, 0xFF) // 1-byte VINT, all value bits set
	return append(out, body...)
}

func ebmlEncodeSize(size int64) []byte {
	if size < 0x7F {
		return []byte{byte(size) | 0x80}
	}
	if size < 0x3FFF {
		return []byte{byte(size>>8) | 0x40, byte(size)}
	}
	return []byte{byte(size>>16) | 0x20, byte(size >> 8), byte(size)}
}

func ebmlFloat64(v float64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(v))
	return buf
}

// ebmlUint encodes an unsigned integer the way Matroska does: minimal bytes, and
// a zero-length body for the value 0 (which is how a first cluster timecode
// appears in real recordings).
func ebmlUint(v uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	return bytes.TrimLeft(buf, "\x00")
}

var (
	idHeader        = []byte{0x1A, 0x45, 0xDF, 0xA3}
	idSegment       = []byte{0x18, 0x53, 0x80, 0x67}
	idInfo          = []byte{0x15, 0x49, 0xA9, 0x66}
	idTimecodeScale = []byte{0x2A, 0xD7, 0xB1}
	idDuration      = []byte{0x44, 0x89}
	idCluster       = []byte{0x1F, 0x43, 0xB6, 0x75}
	idTimecode      = []byte{0xE7}
)

func TestGetWebMDurationFromDeclaredDuration(t *testing.T) {
	// 12345 ticks at the default 1ms scale = 12.345 s.
	info := ebmlElement(idInfo, append(
		ebmlElement(idTimecodeScale, ebmlUint(1_000_000)),
		ebmlElement(idDuration, ebmlFloat64(12345))...,
	))
	file := append(ebmlElement(idHeader, []byte{0x42, 0x86, 0x81, 0x01}), ebmlElement(idSegment, info)...)

	duration, err := GetAudioDuration(context.Background(), bytes.NewReader(file), ".webm")
	require.NoError(t, err)
	assert.InDelta(t, 12.345, duration, 0.001)
}

func TestGetWebMDurationHonoursTimecodeScale(t *testing.T) {
	// A 1ns scale means the same tick count is a far shorter clip; reading the
	// scale is what keeps billing off by orders of magnitude.
	info := ebmlElement(idInfo, append(
		ebmlElement(idTimecodeScale, ebmlUint(1_000)),
		ebmlElement(idDuration, ebmlFloat64(12345))...,
	))
	file := append(ebmlElement(idHeader, []byte{0x42, 0x86, 0x81, 0x01}), ebmlElement(idSegment, info)...)

	duration, err := GetAudioDuration(context.Background(), bytes.NewReader(file), ".webm")
	require.NoError(t, err)
	assert.InDelta(t, 0.012345, duration, 0.000001)
}

// Browser MediaRecorder writes an unknown-size Segment and no Duration, which is
// the shape that previously failed the whole transcription request.
func TestGetWebMDurationFromLiveRecordingWithoutDuration(t *testing.T) {
	info := ebmlElement(idInfo, ebmlElement(idTimecodeScale, ebmlUint(1_000_000)))
	body := info
	for _, timecode := range []uint64{0, 2000, 4000, 6500} {
		body = append(body, ebmlElement(idCluster, ebmlElement(idTimecode, ebmlUint(timecode)))...)
	}
	file := append(ebmlElement(idHeader, []byte{0x42, 0x86, 0x81, 0x01}), ebmlUnknownSizeElement(idSegment, body)...)

	duration, err := GetAudioDuration(context.Background(), bytes.NewReader(file), ".webm")
	require.NoError(t, err, "a live recording without a Duration element must still be measurable")
	assert.InDelta(t, 6.5, duration, 0.001)
}

func TestGetWebMDurationRejectsNonWebM(t *testing.T) {
	_, err := GetAudioDuration(context.Background(), bytes.NewReader([]byte("RIFF....WAVEfmt ")), ".webm")
	assert.Error(t, err)
}

func TestEBMLVIntLength(t *testing.T) {
	assert.Equal(t, 1, ebmlVIntLength(0x80))
	assert.Equal(t, 2, ebmlVIntLength(0x40))
	assert.Equal(t, 4, ebmlVIntLength(0x10))
	assert.Equal(t, 0, ebmlVIntLength(0x00))
}
