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
package router

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A turn that relays and bills but is never recorded is invisible: the
// transcript misses it, the memory never hears it, and nothing anywhere says
// so. /v1/responses is the only route Codex speaks, and it sits outside the
// relay group that carries ChatRecord for every other protocol — so the one
// thing holding the recording on is this handler list. It was lost once
// already, in a merge, and the only symptom was a user reporting that memory
// "doesn't work".
func TestResponsesCreateStillRecordsTheTurn(t *testing.T) {
	handlers, err := taskPluginProtocolHandlers("openai_responses", "create")
	require.NoError(t, err)

	names := make([]string, 0, len(handlers))
	for _, h := range handlers {
		names = append(names, runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name())
	}
	joined := strings.Join(names, "\n")

	require.Contains(t, joined, "ChatRecord",
		"the Codex route stopped recording turns:\n%s", joined)

	// Recording has to wrap the relay, not follow it: the reply is read off the
	// writer this middleware installs, so a later position sees nothing.
	record, relay := -1, -1
	for i, name := range names {
		if strings.Contains(name, "ChatRecord") {
			record = i
		}
		if strings.Contains(name, "Distribute") {
			relay = i
		}
	}
	require.NotEqual(t, -1, record)
	require.NotEqual(t, -1, relay)
	require.Less(t, record, relay, "ChatRecord must run before the relay it wraps")
}
