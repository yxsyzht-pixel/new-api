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
import { describe, it, expect } from 'vitest'

import { granularityForWindow } from '../filters'

const DAY = 86_400
const at = (days: number) => granularityForWindow(0, days * DAY)

describe('granularityForWindow', () => {
  // The view opens on 24 hours; bucketing that by day would draw one bar.
  it('buckets a day or two by hour', () => {
    expect(at(1)).toBe('hour')
    expect(at(2)).toBe('hour')
  })

  // Hourly over a month is 700 points — unreadable, and slow to draw.
  it('buckets weeks and months by day', () => {
    expect(at(3)).toBe('day')
    expect(at(30)).toBe('day')
    expect(at(92)).toBe('day')
  })

  it('buckets anything longer by week', () => {
    expect(at(93)).toBe('week')
    expect(at(365)).toBe('week')
  })

  // The picker tolerates the two ends entered the wrong way round, so this has
  // to as well rather than falling through to the longest bucket.
  it('reads a reversed window the same way', () => {
    expect(granularityForWindow(30 * DAY, 0)).toBe('day')
  })
})
