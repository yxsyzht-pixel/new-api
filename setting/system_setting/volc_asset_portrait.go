package system_setting

import (
	"net/url"
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

// AssetVendorOfChannel names the direct vendor format whose account a
// channel's key belongs to, read from the channel's address: Volcengine Ark
// serves mainland China, BytePlus ModelArk everywhere else. Empty when the
// address is neither vendor's own, such as a proxy or another gateway.
func AssetVendorOfChannel(channelBaseURL string) string {
	host := strings.ToLower(strings.TrimSpace(channelBaseURL))
	if parsed, err := url.Parse(host); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	for suffix, vendor := range map[string]string{
		"bytepluses.com":    AssetFormatBytePlus,
		"byteplusapi.com":   AssetFormatBytePlus,
		"volces.com":        AssetFormatVolcengine,
		"volcengineapi.com": AssetFormatVolcengine,
	} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return vendor
		}
	}
	return ""
}

// OutboundForChannel returns the usable outbound whose assets a channel can
// reference. An outbound bound to the channel comes first. Among those that
// serve every channel, the two vendors' accounts are kept apart: a mainland
// channel never registers in a BytePlus account, nor the other way round, and
// the outbound of the channel's own vendor is preferred. The default outbound
// breaks the remaining ties.
func (v *VolcAssetSettings) OutboundForChannel(channelId int, channelBaseURL string) (AssetOutbound, bool) {
	vendor := AssetVendorOfChannel(channelBaseURL)
	var unbound []AssetOutbound
	for _, o := range v.EffectiveOutbounds() {
		if !v.OutboundConfigured(o) {
			continue
		}
		if len(o.ChannelIds) > 0 {
			if slices.Contains(o.ChannelIds, channelId) {
				return o, true
			}
			continue
		}
		format := o.EffectiveFormat()
		direct := format == AssetFormatVolcengine || format == AssetFormatBytePlus
		if vendor != "" && direct && format != vendor {
			continue
		}
		unbound = append(unbound, o)
	}
	for _, sameVendor := range []bool{true, false} {
		for _, isDefault := range []bool{true, false} {
			for _, o := range unbound {
				if sameVendor && (vendor == "" || o.EffectiveFormat() != vendor) {
					continue
				}
				if isDefault && o.EffectiveId() != v.DefaultOutbound {
					continue
				}
				return o, true
			}
		}
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
