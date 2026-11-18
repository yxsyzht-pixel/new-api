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

import {
  assetLibraryFormValues,
  assetOutboundAddress,
  buildAssetLibraryConfig,
  customAssetFormatIds,
  normalizeActionPrices,
  parseAssetLibraryConfig,
} from '../asset-library-config'

describe('asset library settings', () => {
  // The option the server sends: secrets left out, fields this page never
  // edits included.
  const stored = JSON.stringify({
    outbounds: [
      {
        id: 'byteplus-main',
        format: 'byteplus',
        access_key: 'AK',
        project_name: 'default',
        channel_ids: [3, 5],
      },
      { id: 'old', format: 'volcengine', access_key: 'AK2', disabled: true },
    ],
    default_outbound: 'byteplus-main',
    action_prices: { CreateAsset: 500, GetAsset: 0 },
    one_call: { auto_register: true, wait_seconds: 90 },
    custom_formats: [{ id: 'relay', url_template: '{base_url}/{action}' }],
    some_future_field: { kept: true },
  })

  test('a save without edits stores what was there', () => {
    const config = parseAssetLibraryConfig(stored)
    const built = buildAssetLibraryConfig(
      assetLibraryFormValues(config),
      config
    )
    expect(built.outbounds).toEqual(config.outbounds)
    expect(built.default_outbound).toBe('byteplus-main')
    expect(built.action_prices).toEqual({ CreateAsset: 500 })
    expect(built.custom_formats).toEqual(config.custom_formats)
    expect(built.one_call).toEqual({
      auto_register: true,
      wait_seconds: 90,
      disabled: false,
      reuse_hours: 0,
    })
    expect((built as Record<string, unknown>).some_future_field).toEqual({
      kept: true,
    })
  })

  test('the form reads the stored switches and numbers', () => {
    const values = assetLibraryFormValues(parseAssetLibraryConfig(stored))
    expect(values.oneCallEnabled).toBe(true)
    expect(values.autoRegister).toBe(true)
    expect(values.waitSeconds).toBe(90)
    expect(values.reuseHours).toBe(0)
    expect(values.failover).toBe(false)
    expect(customAssetFormatIds(values.customFormats)).toEqual(['relay'])
  })

  test('switching one-call off and removing the default outbound', () => {
    const config = parseAssetLibraryConfig(stored)
    const values = assetLibraryFormValues(config)
    const built = buildAssetLibraryConfig(
      {
        ...values,
        oneCallEnabled: false,
        outbounds: JSON.stringify([config.outbounds[1]]),
        selectorHeader: '  X-My-Outbound ',
        rateLimitCount: -4,
        reuseHours: -1,
      },
      config
    )
    expect(built.one_call?.disabled).toBe(true)
    expect(built.one_call?.reuse_hours).toBe(-1)
    expect(built.outbounds.map((outbound) => outbound.id)).toEqual(['old'])
    expect(built.default_outbound).toBe('')
    expect(built.outbound_selector_header).toBe('X-My-Outbound')
    expect(built.rate_limit_count).toBe(0)
  })

  test('an option that cannot be read starts empty', () => {
    for (const raw of ['', 'not json', '[]', 'null']) {
      expect(parseAssetLibraryConfig(raw)).toEqual({ outbounds: [] })
    }
    expect(
      parseAssetLibraryConfig('{"outbounds":[{"format":"byteplus"},7]}')
        .outbounds
    ).toEqual([])
  })

  test('only prices that charge something are stored', () => {
    expect(
      normalizeActionPrices(
        '{"CreateAsset":12.9,"GetAsset":0,"DeleteAsset":-3,"ListAssets":"7"}'
      )
    ).toEqual({ CreateAsset: 12 })
    expect(normalizeActionPrices('nonsense')).toEqual({})
  })

  test('the vendor address follows the upstream and region', () => {
    expect(assetOutboundAddress({ id: 'a', format: 'byteplus' })).toBe(
      'https://ark.ap-southeast-1.byteplusapi.com'
    )
    expect(
      assetOutboundAddress({ id: 'a', format: 'byteplus', region: 'eu-west-1' })
    ).toBe('https://ark.eu-west-1.byteplusapi.com')
    expect(assetOutboundAddress({ id: 'a', format: '' })).toBe(
      'https://ark.cn-beijing.volcengineapi.com'
    )
    expect(
      assetOutboundAddress({
        id: 'a',
        format: 'newapi',
        base_url: 'https://gateway.example/volc-asset',
      })
    ).toBe('https://gateway.example/volc-asset')
  })
})
