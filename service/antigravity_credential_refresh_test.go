package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Antigravity access tokens last an hour, so the window between "still fine" and
// "channel is dead" is small; these cases pin where the task draws that line.
func TestAntigravityCredentialNeedsRefresh(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		expired string
		want    bool
	}{
		{"fresh token", now.Add(50 * time.Minute).Format(time.RFC3339), false},
		{"just outside the window", now.Add(16 * time.Minute).Format(time.RFC3339), false},
		{"inside the window", now.Add(14 * time.Minute).Format(time.RFC3339), true},
		{"already expired", now.Add(-time.Minute).Format(time.RFC3339), true},
		{"missing expiry", "", true},
		{"unparseable expiry", "sometime next week", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, antigravityCredentialNeedsRefresh(tc.expired, now))
		})
	}
}

func TestParseAntigravityCredential(t *testing.T) {
	credential, err := ParseAntigravityCredential(
		`  {"access_token":"at","refresh_token":"rt","project_id":"p-1","type":"antigravity"}  `)

	require.NoError(t, err)
	assert.Equal(t, "at", credential.AccessToken)
	assert.Equal(t, "rt", credential.RefreshToken)
	assert.Equal(t, "p-1", credential.ProjectID)

	_, err = ParseAntigravityCredential("   ")
	assert.ErrorContains(t, err, "empty credential")

	_, err = ParseAntigravityCredential("not json")
	assert.ErrorContains(t, err, "invalid credential json")
}
