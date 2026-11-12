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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  ApiKey,
  ApiResponse,
  GetApiKeysParams,
  GetApiKeysResponse,
  SearchApiKeysParams,
  ApiKeyFormData,
  TokenAutoGroupsConfig,
  ApiKeyScope,
  KeyChannelOption,
} from './types'

// ============================================================================
// API Key Management
// ============================================================================

// Get paginated API keys list
export async function getApiKeys(
  params: GetApiKeysParams = {}
): Promise<GetApiKeysResponse> {
  const { p = 1, size = 10, scope } = params
  const queryParams = new URLSearchParams({ p: String(p), size: String(size) })
  if (scope !== undefined) queryParams.set('user_id', String(scope))
  const res = await api.get(`/api/token/?${queryParams.toString()}`)
  return res.data
}

type ChannelPage = {
  success: boolean
  message?: string
  data?: { items?: KeyChannelOption[]; total?: number }
}

/**
 * Every channel, for limiting a key to chosen channels. The channel list is
 * paged at 100, so this walks the pages; it is only ever asked for by someone
 * who may manage everyone's keys.
 */
export async function getKeyChannelOptions(): Promise<KeyChannelOption[]> {
  const pageSize = 100
  const channels: KeyChannelOption[] = []
  for (let page = 1; page <= 50; page++) {
    const res = await api.get<ChannelPage>('/api/channel/', {
      params: { p: page, page_size: pageSize, id_sort: true },
    })
    const items = requireServerSuccess(res.data).data?.items ?? []
    channels.push(
      ...items.map((item) => ({
        id: item.id,
        name: item.name,
        group: item.group,
        status: item.status,
      }))
    )
    const total = res.data.data?.total ?? 0
    if (items.length < pageSize || channels.length >= total) break
  }
  return channels
}

// Search API keys by keyword or token (with pagination)
export async function searchApiKeys(
  params: SearchApiKeysParams
): Promise<GetApiKeysResponse> {
  const { keyword = '', token = '', p, size, scope } = params
  const queryParams = new URLSearchParams()
  if (keyword) queryParams.set('keyword', keyword)
  if (token) queryParams.set('token', token)
  if (scope !== undefined) queryParams.set('user_id', String(scope))
  if (p != null) queryParams.set('p', String(p))
  if (size != null) queryParams.set('size', String(size))
  const res = await api.get(`/api/token/search?${queryParams.toString()}`)
  return res.data
}

// Get single API key by ID
export async function getApiKey(id: number): Promise<ApiResponse<ApiKey>> {
  const res = await api.get(`/api/token/${id}`)
  return res.data
}

// Get the current user's global Auto order and the per-token selection limit.
export async function getTokenAutoGroups(
  // The owner of the key being edited, when that is not the caller. Their
  // selectable groups are what the server validates the choice against.
  ownerUserId?: number
): Promise<ApiResponse<TokenAutoGroupsConfig>> {
  const query = ownerUserId ? `?user_id=${ownerUserId}` : ''
  const res = await api.get(`/api/token/auto-groups${query}`)
  return res.data
}

// Create a new API key
export async function createApiKey(
  data: ApiKeyFormData
): Promise<ApiResponse<ApiKey>> {
  const res = await api.post('/api/token/', data)
  return res.data
}

// Update an existing API key
export async function updateApiKey(
  data: ApiKeyFormData & { id: number }
): Promise<ApiResponse<ApiKey>> {
  const res = await api.put('/api/token/', data)
  return res.data
}

// Delete a single API key
export async function deleteApiKey(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/token/${id}/`)
  return res.data
}

// Batch delete multiple API keys
export async function batchDeleteApiKeys(
  ids: number[]
): Promise<ApiResponse<number>> {
  const res = await api.post('/api/token/batch', { ids })
  return res.data
}

// Update API key status (enable/disable)
export async function updateApiKeyStatus(
  id: number,
  status: number
): Promise<ApiResponse<ApiKey>> {
  const res = await api.put('/api/token/?status_only=true', { id, status })
  return res.data
}

// Fetch the real (unmasked) key for a token by ID
export async function fetchTokenKey(
  id: number
): Promise<{ success: boolean; message?: string; data?: { key: string } }> {
  const res = await api.post(`/api/token/${id}/key`)
  return res.data
}

// Batch fetch real (unmasked) keys for multiple tokens
export async function fetchTokenKeysBatch(ids: number[]): Promise<{
  success: boolean
  message?: string
  data?: { keys: Record<number, string> }
}> {
  const res = await api.post('/api/token/batch/keys', { ids })
  return res.data
}

// Keys are maintained in bulk more often than one at a time. The spreadsheet
// carries the same columns both ways, so an export can be edited and sent back.
export async function exportApiKeys(scope?: ApiKeyScope): Promise<Blob> {
  const query = scope !== undefined ? `?user_id=${scope}` : ''
  const res = await api.get(`/api/token/export${query}`, {
    responseType: 'blob',
  })
  return res.data as Blob
}

export interface ImportApiKeysResult {
  updated: number
  skipped: number
  problems: string[]
}

export async function importApiKeys(
  file: File
): Promise<ApiResponse<ImportApiKeysResult>> {
  const form = new FormData()
  form.append('file', file)
  const res = await api.post('/api/token/import', form)
  return res.data
}

// Issue a new secret for an existing key, keeping its quota, usage history and
// everything else. The replacement is returned once, the same way a newly
// created key is.
export async function resetApiKey(
  id: number
): Promise<ApiResponse<{ key: string }>> {
  const res = await api.post(`/api/token/${id}/reset`, {})
  return res.data
}
