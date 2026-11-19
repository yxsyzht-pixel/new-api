package system_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVolcAssetSettingsActionPrice(t *testing.T) {
	cfg := VolcAssetSettings{ActionPrices: map[string]int{
		"CreateAsset": 1000,
		"GetAsset":    0,
		"DeleteAsset": -5,
	}}

	assert.Equal(t, 1000, cfg.ActionPrice("CreateAsset"))
	// Unconfigured, zero, and negative values are all treated as free.
	assert.Equal(t, 0, cfg.ActionPrice("GetAsset"))
	assert.Equal(t, 0, cfg.ActionPrice("DeleteAsset"))
	assert.Equal(t, 0, cfg.ActionPrice("ListAssets"))

	empty := VolcAssetSettings{}
	assert.Equal(t, 0, empty.ActionPrice("CreateAsset"))
}

func TestVolcAssetSettingsIsConfigured(t *testing.T) {
	tests := []struct {
		name string
		cfg  VolcAssetSettings
		want bool
	}{
		{"no outbounds", VolcAssetSettings{}, false},
		{
			"volcengine complete",
			VolcAssetSettings{Outbounds: []AssetOutbound{{Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk"}}},
			true,
		},
		{
			"volcengine missing secret",
			VolcAssetSettings{Outbounds: []AssetOutbound{{Format: AssetFormatVolcengine, AccessKey: "ak"}}},
			false,
		},
		{
			"newapi complete",
			VolcAssetSettings{Outbounds: []AssetOutbound{{Format: AssetFormatNewAPI, BaseURL: "https://x", AccessToken: "t"}}},
			true,
		},
		{
			"only disabled outbound",
			VolcAssetSettings{Outbounds: []AssetOutbound{{Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk", Disabled: true}}},
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.cfg.IsConfigured())
		})
	}
}

func TestEffectiveOutboundsSkipsDisabled(t *testing.T) {
	cfg := VolcAssetSettings{Outbounds: []AssetOutbound{
		{Id: "a", Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk"},
		{Id: "b", Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk", Disabled: true},
	}}
	obs := cfg.EffectiveOutbounds()
	require.Len(t, obs, 1)
	assert.Equal(t, "a", obs[0].Id)
}

func TestAssetOutboundEffectiveDefaults(t *testing.T) {
	ob := AssetOutbound{}
	assert.Equal(t, AssetFormatVolcengine, ob.EffectiveFormat())
	assert.Equal(t, defaultOutboundId, ob.EffectiveId())
	assert.Equal(t, "cn-beijing", ob.GetRegion())
	assert.Equal(t, "AIGC", ob.GetGroupType())
	assert.Equal(t, "https://ark.cn-beijing.volcengineapi.com", ob.ResolvedBaseURL())

	tok := AssetOutbound{Format: AssetFormatNewAPI, BaseURL: "https://gw/api"}
	assert.Equal(t, "https://gw/api", tok.ResolvedBaseURL())
}

func TestOutboundConfigured(t *testing.T) {
	cfg := VolcAssetSettings{CustomFormats: []AssetCustomFormat{{Id: "myfmt"}}}
	tests := []struct {
		name string
		ob   AssetOutbound
		want bool
	}{
		{"volcengine ok", AssetOutbound{Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk"}, true},
		{"volcengine missing sk", AssetOutbound{Format: AssetFormatVolcengine, AccessKey: "ak"}, false},
		{"newapi ok", AssetOutbound{Format: AssetFormatNewAPI, BaseURL: "https://x", AccessToken: "t"}, true},
		{"custom ok", AssetOutbound{Format: "myfmt", BaseURL: "https://x"}, true},
		{"custom missing base", AssetOutbound{Format: "myfmt"}, false},
		{"custom unknown format", AssetOutbound{Format: "nope", BaseURL: "https://x"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, cfg.OutboundConfigured(tc.ob))
		})
	}
}

func TestResolveOutboundCandidates(t *testing.T) {
	base := func() VolcAssetSettings {
		return VolcAssetSettings{Outbounds: []AssetOutbound{
			{Id: "a", Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk"},
			{Id: "b", Format: AssetFormatNewAPI, BaseURL: "https://b", AccessToken: "t"},
			{Id: "c", Format: AssetFormatVolcengine}, // unconfigured (missing sk), should be filtered out
		}}
	}

	// Client-specified takes priority.
	cfg := base()
	got := cfg.ResolveOutboundCandidates("b")
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].Id)

	// Specified but nonexistent -> fall back to default.
	cfg = base()
	cfg.DefaultOutbound = "b"
	got = cfg.ResolveOutboundCandidates("zzz")
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].Id)

	// Neither specified nor default -> the first one.
	cfg = base()
	got = cfg.ResolveOutboundCandidates("")
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].Id)

	// failover enabled -> primary candidate + other configured outbounds (the unconfigured c is filtered out).
	cfg = base()
	cfg.Failover = true
	got = cfg.ResolveOutboundCandidates("a")
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Id)
	assert.Equal(t, "b", got[1].Id)

	// Empty config -> no candidates.
	assert.Empty(t, (&VolcAssetSettings{}).ResolveOutboundCandidates(""))
}

func TestRedactedClearsOutboundSecrets(t *testing.T) {
	cfg := VolcAssetSettings{
		Outbounds: []AssetOutbound{
			{Id: "a", Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk-a"},
			{Id: "b", Format: AssetFormatNewAPI, AccessToken: "tok-b"},
		},
	}
	got := cfg.Redacted()
	assert.Empty(t, got.Outbounds[0].SecretKey)
	assert.Empty(t, got.Outbounds[1].AccessToken)
	// Non-secret fields are preserved.
	assert.Equal(t, "ak", got.Outbounds[0].AccessKey)
	// Value-receiver semantics: the original object is not modified.
	assert.Equal(t, "sk-a", cfg.Outbounds[0].SecretKey)
}

func TestMergeSecretsRestoresOutboundSecrets(t *testing.T) {
	prev := VolcAssetSettings{Outbounds: []AssetOutbound{
		{Id: "a", SecretKey: "old-sk", AccessToken: "old-tok"},
	}}
	incoming := VolcAssetSettings{Outbounds: []AssetOutbound{
		{Id: "a", SecretKey: "", AccessToken: "new-tok"}, // empty sk should be backfilled from old; new tok overrides
		{Id: "new", SecretKey: ""},                       // prev has no such outbound, stays empty
	}}
	got := incoming.MergeSecretsFromExisting(prev)
	assert.Equal(t, "old-sk", got.Outbounds[0].SecretKey)
	assert.Equal(t, "new-tok", got.Outbounds[0].AccessToken)
	assert.Empty(t, got.Outbounds[1].SecretKey)
}

// BytePlus is the Volcengine protocol on its own domain and region.
func TestBytePlusOutboundDefaults(t *testing.T) {
	ob := AssetOutbound{Format: AssetFormatBytePlus, AccessKey: "ak", SecretKey: "sk"}
	assert.Equal(t, "ap-southeast-1", ob.GetRegion())
	assert.Equal(t, "https://ark.ap-southeast-1.byteplusapi.com", ob.ResolvedBaseURL())
	assert.True(t, IsBuiltinAssetFormat(AssetFormatBytePlus))
	cfg := VolcAssetSettings{Outbounds: []AssetOutbound{ob}}
	assert.True(t, cfg.IsConfigured())
	cfg.Outbounds[0].SecretKey = ""
	assert.False(t, cfg.IsConfigured())

	ob.Region = "eu-west-1"
	assert.Equal(t, "https://ark.eu-west-1.byteplusapi.com", ob.ResolvedBaseURL())
	ob.BaseURL = "https://proxy.example"
	assert.Equal(t, "https://proxy.example", ob.ResolvedBaseURL())

	// The Volcengine format keeps its own region and domain.
	assert.Equal(t, "https://ark.cn-beijing.volcengineapi.com", AssetOutbound{BaseURL: "https://ignored.example"}.ResolvedBaseURL())
}

// A generation request uses the library of the account its channel's key
// belongs to: an outbound bound to the channel, else one that serves every channel.
func TestOutboundForChannel(t *testing.T) {
	usable := func(id string, channels ...int) AssetOutbound {
		return AssetOutbound{Id: id, AccessKey: "ak", SecretKey: "sk", ChannelIds: channels}
	}
	cfg := VolcAssetSettings{
		Outbounds:       []AssetOutbound{usable("shared-a"), usable("bound", 5, 6), usable("shared-b"), {Id: "unusable", ChannelIds: []int{9}}},
		DefaultOutbound: "shared-b",
	}
	for channel, want := range map[int]string{5: "bound", 6: "bound", 7: "shared-b", 9: "shared-b"} {
		ob, ok := cfg.OutboundForChannel(channel, "")
		require.True(t, ok)
		assert.Equal(t, want, ob.EffectiveId(), "channel %d", channel)
	}
	cfg.DefaultOutbound = "bound"
	ob, ok := cfg.OutboundForChannel(7, "")
	require.True(t, ok)
	assert.Equal(t, "shared-a", ob.EffectiveId(), "a bound default serves its own channels only")

	onlyBound := VolcAssetSettings{Outbounds: []AssetOutbound{usable("bound", 5)}}
	_, ok = onlyBound.OutboundForChannel(7, "")
	assert.False(t, ok)
	_, ok = (&VolcAssetSettings{}).OutboundForChannel(7, "")
	assert.False(t, ok)
}

// Mainland China and the rest of the world are two vendors with separate
// accounts: Volcengine Ark and BytePlus ModelArk. With both configured, a
// channel's address decides whose library its requests register in.
func TestOutboundForChannelKeepsTheTwoVendorsApart(t *testing.T) {
	const mainland, international = "https://ark.cn-beijing.volces.com", "https://ark.ap-southeast.bytepluses.com"
	assert.Equal(t, AssetFormatVolcengine, AssetVendorOfChannel(mainland))
	assert.Equal(t, AssetFormatVolcengine, AssetVendorOfChannel("https://ark.cn-beijing.volces.com/api/v3/"))
	assert.Equal(t, AssetFormatBytePlus, AssetVendorOfChannel(international))
	assert.Equal(t, "", AssetVendorOfChannel("https://proxy.example/ark"))
	assert.Equal(t, "", AssetVendorOfChannel("https://notvolces.com"))
	assert.Equal(t, "", AssetVendorOfChannel(""))

	volc := AssetOutbound{Id: "volc", Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk"}
	byteplus := AssetOutbound{Id: "byteplus", Format: AssetFormatBytePlus, AccessKey: "ak", SecretKey: "sk"}
	relay := AssetOutbound{Id: "relay", Format: AssetFormatNewAPI, BaseURL: "https://gateway.example/volc-asset", AccessToken: "t"}
	pick := func(cfg VolcAssetSettings, baseURL string) string {
		ob, ok := cfg.OutboundForChannel(1, baseURL)
		if !ok {
			return ""
		}
		return ob.EffectiveId()
	}

	both := VolcAssetSettings{Outbounds: []AssetOutbound{byteplus, volc}, DefaultOutbound: "byteplus"}
	assert.Equal(t, "volc", pick(both, mainland), "the default outbound does not pull a mainland channel into BytePlus")
	assert.Equal(t, "byteplus", pick(both, international))
	assert.Equal(t, "byteplus", pick(both, "https://proxy.example/ark"), "an address of neither vendor falls back to the default")

	onlyByteplus := VolcAssetSettings{Outbounds: []AssetOutbound{byteplus}}
	assert.Equal(t, "", pick(onlyByteplus, mainland), "no library at all beats the wrong vendor's library")
	assert.Equal(t, "byteplus", pick(onlyByteplus, international))

	// An outbound that is neither vendor's direct account, such as another
	// gateway, may front either; the vendor's own outbound still comes first.
	withRelay := VolcAssetSettings{Outbounds: []AssetOutbound{relay, byteplus}}
	assert.Equal(t, "relay", pick(withRelay, mainland))
	assert.Equal(t, "byteplus", pick(withRelay, international))

	// Binding a channel is the administrator's own word and overrides the address.
	bound := VolcAssetSettings{Outbounds: []AssetOutbound{{Id: "volc", Format: AssetFormatVolcengine, AccessKey: "ak", SecretKey: "sk", ChannelIds: []int{1}}, byteplus}}
	assert.Equal(t, "volc", pick(bound, international))
}

func TestOneCallDefaults(t *testing.T) {
	assert.Equal(t, 120*time.Second, AssetOneCall{}.Wait())
	assert.Equal(t, 30*time.Second, AssetOneCall{WaitSeconds: 30}.Wait())
	assert.Equal(t, 24*time.Hour, AssetOneCall{}.Reuse())
	assert.Equal(t, time.Hour, AssetOneCall{ReuseHours: 1}.Reuse())
	assert.Zero(t, AssetOneCall{ReuseHours: -1}.Reuse())
}

func TestLivenessCallbackURL(t *testing.T) {
	previous := ServerAddress
	t.Cleanup(func() { ServerAddress = previous })
	for address, want := range map[string]string{
		"https://gateway.example/": "https://gateway.example/volc-asset/VisualValidateCallback",
		"http://localhost:3000":    "",
		"":                         "",
	} {
		ServerAddress = address
		assert.Equal(t, want, LivenessCallbackURL(), address)
	}
}

func TestOneCallQueueWaitDefault(t *testing.T) {
	assert.Equal(t, 10*time.Minute, AssetOneCall{}.QueueWait())
	assert.Equal(t, 90*time.Second, AssetOneCall{QueueWaitSeconds: 90}.QueueWait())
}

func TestMediaStorageDefaults(t *testing.T) {
	assert.False(t, AssetMediaStorage{}.Enabled())
	storage := AssetMediaStorage{Provider: MediaStorageQiniu, AccessKey: "ak", SecretKey: "sk", Bucket: "media", Domain: " cdn.example.com/ "}
	assert.True(t, storage.Enabled())
	assert.Equal(t, "https://cdn.example.com", storage.BaseURL(), "a bare host is served over https")
	assert.Equal(t, "gateway-media", storage.KeyPrefix())
	assert.Equal(t, int64(64)<<20, storage.MaxFileBytes())
	assert.Equal(t, 24*time.Hour, storage.URLTTL())

	storage.Domain, storage.Prefix, storage.MaxFileMB, storage.URLTTLHours = "http://cdn.example.com", "/uploads/video/", 5, 2
	assert.Equal(t, "http://cdn.example.com", storage.BaseURL())
	assert.Equal(t, "uploads/video", storage.KeyPrefix())
	assert.Equal(t, int64(5)<<20, storage.MaxFileBytes())
	assert.Equal(t, 2*time.Hour, storage.URLTTL())

	for name, incomplete := range map[string]AssetMediaStorage{
		"no provider": {AccessKey: "ak", SecretKey: "sk", Bucket: "media", Domain: "cdn.example.com"},
		"no secret":   {Provider: MediaStorageQiniu, AccessKey: "ak", Bucket: "media", Domain: "cdn.example.com"},
		"no bucket":   {Provider: MediaStorageQiniu, AccessKey: "ak", SecretKey: "sk", Domain: "cdn.example.com"},
		"no domain":   {Provider: MediaStorageQiniu, AccessKey: "ak", SecretKey: "sk", Bucket: "media"},
	} {
		assert.False(t, incomplete.Enabled(), name)
	}
}

// The storage secret is never echoed to the settings page, and saving the
// page without retyping it keeps the stored one.
func TestMediaStorageSecretIsRedactedAndKept(t *testing.T) {
	stored := VolcAssetSettings{MediaStorage: AssetMediaStorage{Provider: MediaStorageQiniu, AccessKey: "ak", SecretKey: "sk", Bucket: "media"}}
	redacted := stored.Redacted()
	assert.Empty(t, redacted.MediaStorage.SecretKey)
	assert.Equal(t, "ak", redacted.MediaStorage.AccessKey)
	assert.Equal(t, "sk", stored.MediaStorage.SecretKey, "the stored settings are untouched")

	assert.Equal(t, "sk", redacted.MergeSecretsFromExisting(stored).MediaStorage.SecretKey)
	retyped := redacted
	retyped.MediaStorage.SecretKey = "new"
	assert.Equal(t, "new", retyped.MergeSecretsFromExisting(stored).MediaStorage.SecretKey)
}
