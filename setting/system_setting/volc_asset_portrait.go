package system_setting

import (
	"slices"
	"strings"
	"time"
)

const (
	defaultOneCallWaitSeconds = 120
	defaultOneCallReuseHours  = 24
)

// AssetOneCall configures registering a generation request's portrait
// references in the asset library before the request is sent.
type AssetOneCall struct {
	// Disabled refuses portrait references in generation requests; callers
	// register assets themselves and send asset:// references.
	Disabled bool `json:"disabled,omitempty"`
	// AutoRegister treats every image and video reference of a Seedance 2
	// request as a virtual portrait when the request does not say otherwise.
	AutoRegister bool `json:"auto_register,omitempty"`
	// WaitSeconds bounds how long one request waits for its assets to become
	// usable; <=0 means the default.
	WaitSeconds int `json:"wait_seconds,omitempty"`
	// ReuseHours is how long a registered URL keeps answering with the same
	// asset; 0 means the default and a negative value registers every time.
	ReuseHours int `json:"reuse_hours,omitempty"`
}

// Wait is how long one generation request waits for its assets.
func (o AssetOneCall) Wait() time.Duration {
	if o.WaitSeconds <= 0 {
		return defaultOneCallWaitSeconds * time.Second
	}
	return time.Duration(o.WaitSeconds) * time.Second
}

// Reuse is how long a registered URL is answered from the asset already made
// for it; zero means never.
func (o AssetOneCall) Reuse() time.Duration {
	switch {
	case o.ReuseHours < 0:
		return 0
	case o.ReuseHours == 0:
		return defaultOneCallReuseHours * time.Hour
	}
	return time.Duration(o.ReuseHours) * time.Hour
}

// OutboundForChannel returns the usable outbound whose assets a channel can
// reference: one bound to the channel first, then the default outbound and
// the remaining ones when they serve every channel.
func (v *VolcAssetSettings) OutboundForChannel(channelId int) (AssetOutbound, bool) {
	var unbound []AssetOutbound
	for _, o := range v.EffectiveOutbounds() {
		if !v.OutboundConfigured(o) {
			continue
		}
		if len(o.ChannelIds) == 0 {
			unbound = append(unbound, o)
			continue
		}
		if slices.Contains(o.ChannelIds, channelId) {
			return o, true
		}
	}
	for _, o := range unbound {
		if o.EffectiveId() == v.DefaultOutbound {
			return o, true
		}
	}
	if len(unbound) > 0 {
		return unbound[0], true
	}
	return AssetOutbound{}, false
}

// OutboundById returns the usable outbound with the given Id.
func (v *VolcAssetSettings) OutboundById(id string) (AssetOutbound, bool) {
	for _, o := range v.EffectiveOutbounds() {
		if o.EffectiveId() == id && v.OutboundConfigured(o) {
			return o, true
		}
	}
	return AssetOutbound{}, false
}

// LivenessCallbackURL is the gateway's own page a liveness check returns to,
// or empty when the server address is not a public one.
func LivenessCallbackURL() string {
	address := strings.TrimRight(strings.TrimSpace(ServerAddress), "/")
	lower := strings.ToLower(address)
	if address == "" || strings.HasPrefix(lower, "http://localhost") || strings.HasPrefix(lower, "http://127.") {
		return ""
	}
	return address + "/volc-asset/VisualValidateCallback"
}
