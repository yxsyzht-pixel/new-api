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
import assert from 'node:assert/strict'
import { describe, test } from 'vitest'

import type { QuotaDataItem } from '../../types'
import { processUserChartData } from '../charts'

const rows: QuotaDataItem[] = [
  {
    username: 'amount-leader',
    created_at: 1_786_000_000,
    quota: 5_000,
    token_used: 10,
  },
  {
    username: 'token-leader',
    created_at: 1_786_000_000,
    quota: 100,
    token_used: 2_000,
  },
]

function rankValues(metric: 'quota' | 'tokens') {
  const result = processUserChartData(rows, 'day', undefined, 10, metric)
  return result.spec_user_rank.data[0].values as Array<{
    User: string
    rawValue: number
  }>
}

describe('user analytics metric', () => {
  test('ranks users by billed amount when amount is selected', () => {
    assert.deepEqual(rankValues('quota'), [
      { User: 'amount-leader', rawValue: 5_000 },
      { User: 'token-leader', rawValue: 100 },
    ])
  })

  test('ranks users and builds trend values from token usage when Tokens is selected', () => {
    const result = processUserChartData(rows, 'day', undefined, 10, 'tokens')
    const rank = result.spec_user_rank.data[0].values
    const trend = result.spec_user_trend.data[0].values

    assert.deepEqual(rank, [
      { User: 'token-leader', rawValue: 2_000 },
      { User: 'amount-leader', rawValue: 10 },
    ])
    assert.deepEqual(
      trend.map((item: { User: string; rawValue: number }) => ({
        User: item.User,
        rawValue: item.rawValue,
      })),
      [
        { User: 'token-leader', rawValue: 2_000 },
        { User: 'amount-leader', rawValue: 10 },
      ]
    )
    assert.match(result.spec_user_rank.title.subtext, /Tokens$/)
  })

  test('uses Chinese compact units for large token values', () => {
    const result = processUserChartData(
      [
        {
          username: 'large-token-user',
          created_at: 1_786_000_000,
          quota: 0,
          token_used: 1_589_345_535,
        },
      ],
      'day',
      undefined,
      10,
      'tokens'
    )
    const labelFormatter = result.spec_user_rank.label.formatMethod
    const tooltipFormatter = result.spec_user_rank.tooltip.mark.content[0].value

    assert.equal(result.spec_user_rank.title.subtext, 'Total: 15.89 亿 Tokens')
    assert.equal(labelFormatter(367_564_110), '3.68 亿')
    assert.equal(labelFormatter(80_000_000), '8,000 万')
    assert.equal(
      tooltipFormatter({ rawValue: 367_564_110 }),
      '3.68 亿 Tokens（367,564,110）'
    )
  })
})
