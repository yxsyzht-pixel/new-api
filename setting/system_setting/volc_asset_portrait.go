package system_setting

import (
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	defaultOneCallWaitSeconds      = 120
	defaultOneCallReuseHours       = 24
	defaultOneCallQueueWaitSeconds = 600
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
	// DeleteAfterUse removes an asset a generation request registered once no
	// unfinished task references it, so a small library does not fill up.
	DeleteAfterUse bool `json:"delete_after_use,omitempty"`
	// MaxAssets is how many assets the library of one outbound may hold; a
	// request that needs more room waits for it. <=0 means no limit.
	MaxAssets int `json:"max_assets,omitempty"`
	// UploadsPerMinute bounds how many assets one outbound registers per
	// minute; <=0 means no limit.
	UploadsPerMinute int `json:"uploads_per_minute,omitempty"`
	// QueueWaitSeconds bounds how long one request waits for room in the
	// library or for an upload slot; <=0 means the default.
	QueueWaitSeconds int `json:"queue_wait_seconds,omitempty"`
}

// QueueWait is how long one generation request waits for room in the library.
func (o AssetOneCall) QueueWait() time.Duration {
	if o.QueueWaitSeconds <= 0 {
		return defaultOneCallQueueWaitSeconds * time.Second
	}
	return time.Duration(o.QueueWaitSeconds) * time.Second
}

const (
	// MediaStorageQiniu is the Qiniu Kodo object storage provider.
	MediaStorageQiniu = "qiniu"

	defaultMediaStoragePrefix    = "gateway-media"
	defaultMediaStorageMaxFileMB = 64
	defaultMediaStorageURLHours  = 24
)

// AssetMediaStorage is the object storage that receives the files a
// generation request carries inline (a data: URL or an uploaded file) and
// hands back a URL the vendor can fetch.
type AssetMediaStorage struct {
	// Provider is the storage service; empty switches inline files off.
	Provider  string `json:"provider,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	// Domain is the address the bucket is served at, such as https://cdn.example.com.
	Domain string `json:"domain,omitempty"`
	// Prefix is put before every object key; empty means the default.
	Prefix string `json:"prefix,omitempty"`
	// UploadURL overrides the upload address; empty looks it up from the bucket.
	UploadURL string `json:"upload_url,omitempty"`
	// Private signs every file URL, for a bucket that is not public.
	Private bool `json:"private,omitempty"`
	// URLTTLHours is how long a signed URL stays valid; <=0 means the default.
	URLTTLHours int `json:"url_ttl_hours,omitempty"`
	// MaxFileMB bounds one inline file; <=0 means the default.
	MaxFileMB int `json:"max_file_mb,omitempty"`
}

// Enabled reports whether inline files can be uploaded.
func (s AssetMediaStorage) Enabled() bool {
	return s.Provider == MediaStorageQiniu && s.AccessKey != "" && s.SecretKey != "" && s.Bucket != "" && s.BaseURL() != ""
}

// BaseURL is the address files are served at, without a trailing slash.
func (s AssetMediaStorage) BaseURL() string {
	domain := strings.TrimRight(strings.TrimSpace(s.Domain), "/")
	if domain == "" {
		return ""
	}
	lower := strings.ToLower(domain)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		domain = "https://" + domain
	}
	return domain
}

// KeyPrefix is the path every object key starts with, without slashes around it.
func (s AssetMediaStorage) KeyPrefix() string {
	prefix := strings.Trim(strings.TrimSpace(s.Prefix), "/")
	if prefix == "" {
		return defaultMediaStoragePrefix
	}
	return prefix
}

// MaxFileBytes is the largest inline file accepted.
func (s AssetMediaStorage) MaxFileBytes() int64 {
	limit := s.MaxFileMB
	if limit <= 0 {
		limit = defaultMediaStorageMaxFileMB
	}
	return int64(limit) << 20
}

// URLTTL is how long a signed file URL stays valid.
func (s AssetMediaStorage) URLTTL() time.Duration {
	hours := s.URLTTLHours
	if hours <= 0 {
		hours = defaultMediaStorageURLHours
	}
	return time.Duration(hours) * time.Hour
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
