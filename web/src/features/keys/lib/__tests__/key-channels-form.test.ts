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
import { describe, expect, test } from 'vitest'

import { apiKeySchema } from '../../types'
import {
  API_KEY_FORM_DEFAULT_VALUES,
  transformApiKeyToFormDefaults,
  transformFormDataToPayload,
} from '../api-key-form'

const storedKey = apiKeySchema.parse({
  id: 7,
  user_id: 47,
  key: 'sk-****',
  status: 1,
  name: 'routed',
  remain_quota: 0,
  used_quota: 0,
  unlimited_quota: true,
  expired_time: -1,
  created_time: 0,
  accessed_time: 0,
  model_limits_enabled: false,
  channel_ids: [4, 9],
})

describe('key channel form mapping', () => {
  test('loads the channels stored on a key into the multi-select', () => {
    expect(transformApiKeyToFormDefaults(storedKey).channel_ids).toEqual([
      '4',
      '9',
    ])
  })

  test('starts a key that has no channels with none selected', () => {
    const plain = apiKeySchema.parse({ ...storedKey, channel_ids: null })

    expect(transformApiKeyToFormDefaults(plain).channel_ids).toEqual([])
  })

  test('sends the chosen channels as numbers and drops anything that is not an id', () => {
    const payload = transformFormDataToPayload({
      ...API_KEY_FORM_DEFAULT_VALUES,
      name: 'routed',
      channel_ids: ['9', '4', 'x', '0'],
    })

    expect(payload.channel_ids).toEqual([9, 4])
  })
})
