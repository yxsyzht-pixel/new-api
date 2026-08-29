package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// 额度展示类型
const (
	QuotaDisplayTypeUSD    = "USD"
	QuotaDisplayTypeCNY    = "CNY"
	QuotaDisplayTypeTokens = "TOKENS"
	QuotaDisplayTypeCustom = "CUSTOM"
)

type GeneralSetting struct {
	DocsLink            string `json:"docs_link"`
	PingIntervalEnabled bool   `json:"ping_interval_enabled"`
	PingIntervalSeconds int    `json:"ping_interval_seconds"`
	// ProgressHeartbeat replaces the SSE comment with a real protocol event
	// once an upstream has gone quiet, for clients that only count events.
	//
	// Codex is one: the gateway pings every five seconds and those writes
	// succeed, yet it still gave up on 162 streams over three days — 23 hours
	// of waiting, 37% of them abandoned rather than retried — reporting "idle
	// timeout waiting for SSE" against a socket that was never idle. A comment
	// keeps the connection warm; it does not appear to keep the client's timer
	// alive.
	//
	// Off by default. Sending an event the upstream did not send is a liberty,
	// and whether a given client accepts a repeated one can only be found out
	// by trying it against that client.
	ProgressHeartbeatEnabled bool `json:"progress_heartbeat_enabled"`
	// ProgressHeartbeatAfterSeconds is how long an upstream must say nothing
	// before the heartbeat starts. Well past a normal first byte — the median
	// here is 2.5 seconds — so ordinary requests never reach it.
	ProgressHeartbeatAfterSeconds int `json:"progress_heartbeat_after_seconds"`
	// 当前站点额度展示类型：USD / CNY / TOKENS
	QuotaDisplayType string `json:"quota_display_type"`
	// 自定义货币符号，用于 CUSTOM 展示类型
	CustomCurrencySymbol string `json:"custom_currency_symbol"`
	// 自定义货币与美元汇率（1 USD = X Custom）
	CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
	// 上游返回用量超限（429）后，该渠道暂停参与选择的秒数，到期自动恢复
	ChannelUsageLimitCooldownSeconds int `json:"channel_usage_limit_cooldown_seconds"`
}

// 默认配置
var generalSetting = GeneralSetting{
	DocsLink:                      "https://docs.newapi.pro",
	PingIntervalEnabled:           false,
	PingIntervalSeconds:           60,
	ProgressHeartbeatEnabled:      false,
	ProgressHeartbeatAfterSeconds: 60,
	QuotaDisplayType:              QuotaDisplayTypeUSD,
	CustomCurrencySymbol:          "¤",
	CustomCurrencyExchangeRate:    1.0,
	// 3 分钟足够跨过大多数上游滚动窗口的抖动，又不会让恢复后的额度长时间闲置
	ChannelUsageLimitCooldownSeconds: 180,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("general_setting", &generalSetting)
}

func GetGeneralSetting() *GeneralSetting {
	return &generalSetting
}

// IsCurrencyDisplay 是否以货币形式展示（美元或人民币）
func IsCurrencyDisplay() bool {
	return generalSetting.QuotaDisplayType != QuotaDisplayTypeTokens
}

// IsCNYDisplay 是否以人民币展示
func IsCNYDisplay() bool {
	return generalSetting.QuotaDisplayType == QuotaDisplayTypeCNY
}

// GetQuotaDisplayType 返回额度展示类型
func GetQuotaDisplayType() string {
	return generalSetting.QuotaDisplayType
}

// GetCurrencySymbol 返回当前展示类型对应符号
func GetCurrencySymbol() string {
	switch generalSetting.QuotaDisplayType {
	case QuotaDisplayTypeUSD:
		return "$"
	case QuotaDisplayTypeCNY:
		return "¥"
	case QuotaDisplayTypeCustom:
		if generalSetting.CustomCurrencySymbol != "" {
			return generalSetting.CustomCurrencySymbol
		}
		return "¤"
	default:
		return ""
	}
}

// GetUsdToCurrencyRate 返回 1 USD = X <currency> 的 X（TOKENS 不适用）
func GetUsdToCurrencyRate(usdToCny float64) float64 {
	switch generalSetting.QuotaDisplayType {
	case QuotaDisplayTypeUSD:
		return 1
	case QuotaDisplayTypeCNY:
		return usdToCny
	case QuotaDisplayTypeCustom:
		if generalSetting.CustomCurrencyExchangeRate > 0 {
			return generalSetting.CustomCurrencyExchangeRate
		}
		return 1
	default:
		return 1
	}
}
