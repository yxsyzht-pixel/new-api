/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package service

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The retry loop records every channel it attempts, and the selectors read that
// record back to avoid handing one of them over twice. It was written for a log
// line, so nothing was checking it could be read as a set.
func TestRetryParamTriedChannels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)

	assert.Nil(t, (&RetryParam{Ctx: ctx}).triedChannels(),
		"a first attempt has tried nothing")
	assert.Nil(t, (&RetryParam{}).triedChannels(),
		"no context is not a panic")
	var nilParam *RetryParam
	assert.Nil(t, nilParam.triedChannels())

	ctx.Set("use_channel", []string{"21", "4", "21"})
	tried := (&RetryParam{Ctx: ctx}).triedChannels()
	require.NotNil(t, tried)
	assert.True(t, tried[21])
	assert.True(t, tried[4])
	assert.False(t, tried[7], "a channel never attempted must stay available")
	assert.Len(t, tried, 2, "the same channel twice is still one channel")

	// addUsedChannel writes decimal strings, but a malformed entry must not take
	// the healthy ones down with it.
	ctx.Set("use_channel", []string{"12", "not-a-channel"})
	tried = (&RetryParam{Ctx: ctx}).triedChannels()
	assert.True(t, tried[12])
	assert.Len(t, tried, 1)
}
