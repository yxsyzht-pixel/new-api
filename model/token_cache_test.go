package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func useTokenCacheMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	previousEnabled := common.RedisEnabled
	previousClient := common.RDB
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())

	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled = previousEnabled
		common.RDB = previousClient
	})

	return server
}

// TestTokenCacheRoundTripsRequestPathFields guards the one failure mode a
// hand-written HSET field list has: a field that exists on the struct, is read
// on the request path, and is simply absent from the script. Nothing errors —
// the field comes back as its zero value, and for an opt-out flag the zero
// value is the opposite of what the key was configured to do. That is how
// keys with "记录聊天内容" turned off went on being recorded on every request
// that hit a warm cache.
func TestTokenCacheRoundTripsRequestPathFields(t *testing.T) {
	useTokenCacheMiniRedis(t)

	allowIps := "10.0.0.0/8"
	stored := Token{
		Id:                 4242,
		UserId:             7,
		Key:                "round-trip-key",
		Status:             common.TokenStatusEnabled,
		Name:               "zht-macmini",
		StaffId:            "LS000041",
		SkipChatRecord:     true,
		SkipMemory:         true,
		CreatedTime:        1_700_000_000,
		AccessedTime:       1_700_000_500,
		ExpiredTime:        -1,
		RemainQuota:        1234,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: true,
		ModelLimits:        "gpt-5.6-sol",
		AllowIps:           &allowIps,
		UsedQuota:          99,
		Group:              "default",
		CrossGroupRetry:    true,
		AutoGroups:         `["a","b"]`,
	}

	result, err := cacheInitToken(stored)
	require.NoError(t, err)
	require.Equal(t, 1, result, "cold cache should publish the snapshot")

	loaded, err := cacheGetTokenByKey(stored.Key)
	require.NoError(t, err)

	// The opt-outs first: these are the ones whose absence is silent.
	require.True(t, loaded.SkipChatRecord, "SkipChatRecord must survive the cache")
	require.True(t, loaded.SkipMemory, "SkipMemory must survive the cache")
	require.Equal(t, stored.StaffId, loaded.StaffId, "StaffId must survive the cache")

	require.Equal(t, stored.Id, loaded.Id)
	require.Equal(t, stored.UserId, loaded.UserId)
	require.Equal(t, stored.Status, loaded.Status)
	require.Equal(t, stored.Name, loaded.Name)
	require.Equal(t, stored.ExpiredTime, loaded.ExpiredTime)
	require.Equal(t, stored.RemainQuota, loaded.RemainQuota)
	require.Equal(t, stored.UnlimitedQuota, loaded.UnlimitedQuota)
	require.Equal(t, stored.ModelLimitsEnabled, loaded.ModelLimitsEnabled)
	require.Equal(t, stored.ModelLimits, loaded.ModelLimits)
	require.Equal(t, stored.Group, loaded.Group)
	require.Equal(t, stored.CrossGroupRetry, loaded.CrossGroupRetry)
	require.Equal(t, stored.AutoGroups, loaded.AutoGroups)
	require.NotNil(t, loaded.AllowIps)
	require.Equal(t, allowIps, *loaded.AllowIps)
}
