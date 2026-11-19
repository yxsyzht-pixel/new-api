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
/**
 * The asset gateway's settings as the VolcAssetConfig option stores them.
 * Fields this page does not edit ride along untouched.
 */
export type AssetOutbound = {
  id: string
  name?: string
  format: string
  base_url?: string
  region?: string
  project_name?: string
  group_type?: string
  access_key?: string
  secret_key?: string
  access_token?: string
  disabled?: boolean
  channel_ids?: number[]
}

export type AssetOneCall = {
  disabled?: boolean
  auto_register?: boolean
  wait_seconds?: number
  reuse_hours?: number
  delete_after_use?: boolean
  max_assets?: number
  uploads_per_minute?: number
  queue_wait_seconds?: number
}

/** Where the files a generation request carries inline are uploaded. */
export type AssetMediaStorage = {
  provider?: string
  access_key?: string
  secret_key?: string
  bucket?: string
  domain?: string
  prefix?: string
  upload_url?: string
  private?: boolean
  url_ttl_hours?: number
  max_file_mb?: number
}

export type AssetLibraryConfig = {
  outbounds: AssetOutbound[]
  default_outbound?: string
  outbound_selector_header?: string
  failover?: boolean
  custom_formats?: unknown[]
  action_prices?: Record<string, number>
  rate_limit_count?: number
  rate_limit_duration_seconds?: number
  one_call?: AssetOneCall
  media_storage?: AssetMediaStorage
}

export type AssetLibraryFormValues = {
  outbounds: string
  defaultOutbound: string
  selectorHeader: string
  failover: boolean
  oneCallEnabled: boolean
  autoRegister: boolean
  waitSeconds: number
  reuseHours: number
  deleteAfterUse: boolean
  maxAssets: number
  uploadsPerMinute: number
  queueWaitSeconds: number
  storageEnabled: boolean
  storageAccessKey: string
  storageSecretKey: string
  storageBucket: string
  storageDomain: string
  storagePrefix: string
  storagePrivate: boolean
  storageMaxFileMb: number
  rateLimitCount: number
  rateLimitDurationSeconds: number
  actionPrices: string
  customFormats: string
}

export const ASSET_FORMAT_VOLCENGINE = 'volcengine'
export const ASSET_FORMAT_BYTEPLUS = 'byteplus'
export const ASSET_FORMAT_NEWAPI = 'newapi'

export const BUILTIN_ASSET_FORMATS = [
  ASSET_FORMAT_VOLCENGINE,
  ASSET_FORMAT_BYTEPLUS,
  ASSET_FORMAT_NEWAPI,
] as const

/** The operations a price can be set for, in the order the page lists them. */
export const ASSET_PRICED_ACTIONS = [
  'CreateAsset',
  'ListAssets',
  'GetAsset',
  'UpdateAsset',
  'DeleteAsset',
  'CreateVisualValidateSession',
  'GetVisualValidateResult',
  'ListLivenessGroups',
] as const

export const ASSET_ONE_CALL_DEFAULT_WAIT_SECONDS = 120
export const ASSET_ONE_CALL_DEFAULT_REUSE_HOURS = 24
export const ASSET_ONE_CALL_DEFAULT_QUEUE_WAIT_SECONDS = 600

export const ASSET_MEDIA_STORAGE_QINIU = 'qiniu'
export const ASSET_MEDIA_STORAGE_DEFAULT_PREFIX = 'gateway-media'
export const ASSET_MEDIA_STORAGE_DEFAULT_MAX_FILE_MB = 64

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseJson(raw: string): unknown {
  try {
    return JSON.parse(raw)
  } catch {
    return undefined
  }
}

function wholeNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.trunc(value)
    : 0
}

/** Reads the option; anything unreadable becomes an empty configuration. */
export function parseAssetLibraryConfig(raw: string): AssetLibraryConfig {
  const parsed = parseJson((raw ?? '').trim() || '{}')
  if (!isRecord(parsed)) return { outbounds: [] }
  const outbounds = Array.isArray(parsed.outbounds)
    ? parsed.outbounds.filter(
        (item): item is AssetOutbound =>
          isRecord(item) && typeof item.id === 'string'
      )
    : []
  return { ...(parsed as AssetLibraryConfig), outbounds }
}

export function parseAssetOutbounds(raw: string): AssetOutbound[] {
  const parsed = parseJson(raw)
  if (!Array.isArray(parsed)) return []
  return parsed.filter(
    (item): item is AssetOutbound =>
      isRecord(item) && typeof item.id === 'string'
  )
}

/** Ids of the custom outbound formats, for the format choice of an outbound. */
export function customAssetFormatIds(raw: string): string[] {
  const parsed = parseJson(raw)
  if (!Array.isArray(parsed)) return []
  return parsed
    .filter(isRecord)
    .map((item) => item.id)
    .filter((id): id is string => typeof id === 'string' && id !== '')
}

export function assetLibraryFormValues(
  config: AssetLibraryConfig
): AssetLibraryFormValues {
  const oneCall = config.one_call ?? {}
  const storage = config.media_storage ?? {}
  return {
    outbounds: JSON.stringify(config.outbounds ?? [], null, 2),
    defaultOutbound: config.default_outbound ?? '',
    selectorHeader: config.outbound_selector_header ?? '',
    failover: Boolean(config.failover),
    oneCallEnabled: !oneCall.disabled,
    autoRegister: Boolean(oneCall.auto_register),
    waitSeconds: wholeNumber(oneCall.wait_seconds),
    reuseHours: wholeNumber(oneCall.reuse_hours),
    deleteAfterUse: Boolean(oneCall.delete_after_use),
    maxAssets: wholeNumber(oneCall.max_assets),
    uploadsPerMinute: wholeNumber(oneCall.uploads_per_minute),
    queueWaitSeconds: wholeNumber(oneCall.queue_wait_seconds),
    storageEnabled: storage.provider === ASSET_MEDIA_STORAGE_QINIU,
    storageAccessKey: storage.access_key ?? '',
    storageSecretKey: storage.secret_key ?? '',
    storageBucket: storage.bucket ?? '',
    storageDomain: storage.domain ?? '',
    storagePrefix: storage.prefix ?? '',
    storagePrivate: Boolean(storage.private),
    storageMaxFileMb: wholeNumber(storage.max_file_mb),
    rateLimitCount: wholeNumber(config.rate_limit_count),
    rateLimitDurationSeconds: wholeNumber(config.rate_limit_duration_seconds),
    actionPrices: JSON.stringify(config.action_prices ?? {}),
    customFormats: JSON.stringify(config.custom_formats ?? [], null, 2),
  }
}

/** Keeps the prices that charge something; a missing price is free. */
export function normalizeActionPrices(raw: string): Record<string, number> {
  const parsed = parseJson(raw)
  const prices: Record<string, number> = {}
  if (!isRecord(parsed)) return prices
  for (const [action, price] of Object.entries(parsed)) {
    const amount = wholeNumber(price)
    if (amount > 0) prices[action] = amount
  }
  return prices
}

/**
 * The option value for a form. The form's values replace what the page
 * edits; whatever else the stored configuration holds stays.
 */
export function buildAssetLibraryConfig(
  values: AssetLibraryFormValues,
  stored: AssetLibraryConfig
): AssetLibraryConfig {
  const outbounds = parseAssetOutbounds(values.outbounds)
  const customFormats = parseJson(values.customFormats)
  const defaultOutbound = outbounds.some(
    (outbound) => outbound.id === values.defaultOutbound
  )
    ? values.defaultOutbound
    : ''
  return {
    ...stored,
    outbounds,
    default_outbound: defaultOutbound,
    outbound_selector_header: values.selectorHeader.trim(),
    failover: values.failover,
    custom_formats: Array.isArray(customFormats) ? customFormats : [],
    action_prices: normalizeActionPrices(values.actionPrices),
    rate_limit_count: Math.max(0, wholeNumber(values.rateLimitCount)),
    rate_limit_duration_seconds: Math.max(
      0,
      wholeNumber(values.rateLimitDurationSeconds)
    ),
    one_call: {
      ...stored.one_call,
      disabled: !values.oneCallEnabled,
      auto_register: values.autoRegister,
      wait_seconds: Math.max(0, wholeNumber(values.waitSeconds)),
      reuse_hours: wholeNumber(values.reuseHours),
      delete_after_use: values.deleteAfterUse,
      max_assets: Math.max(0, wholeNumber(values.maxAssets)),
      uploads_per_minute: Math.max(0, wholeNumber(values.uploadsPerMinute)),
      queue_wait_seconds: Math.max(0, wholeNumber(values.queueWaitSeconds)),
    },
    media_storage: {
      ...stored.media_storage,
      provider: values.storageEnabled ? ASSET_MEDIA_STORAGE_QINIU : '',
      access_key: values.storageAccessKey.trim(),
      // An empty secret keeps the one the server holds.
      secret_key: values.storageSecretKey.trim(),
      bucket: values.storageBucket.trim(),
      domain: values.storageDomain.trim(),
      prefix: values.storagePrefix.trim(),
      private: values.storagePrivate,
      max_file_mb: Math.max(0, wholeNumber(values.storageMaxFileMb)),
    },
  }
}

/** The vendor address an outbound reaches, as the gateway composes it. */
export function assetOutboundAddress(outbound: AssetOutbound): string {
  const format = outbound.format || ASSET_FORMAT_VOLCENGINE
  if (format === ASSET_FORMAT_VOLCENGINE) {
    return `https://ark.${outbound.region || 'cn-beijing'}.volcengineapi.com`
  }
  if (format === ASSET_FORMAT_BYTEPLUS && !outbound.base_url) {
    return `https://ark.${outbound.region || 'ap-southeast-1'}.byteplusapi.com`
  }
  return outbound.base_url ?? ''
}
