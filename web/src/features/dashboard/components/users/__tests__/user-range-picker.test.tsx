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
import { render, screen } from '@testing-library/react'
import { describe, it, expect } from 'vitest'

import { UserRangePicker } from '../user-range-picker'

describe('UserRangePicker', () => {
  // The presets are gone: the window is the date field alone, so there is no
  // second control that can sit on a range the charts are not showing.
  it('offers the window as one field, with no preset tabs', () => {
    render(
      <UserRangePicker
        customStart={1_700_000_000}
        customEnd={1_700_600_000}
        onCustomChange={() => {}}
      />
    )
    expect(screen.queryAllByRole('tab')).toHaveLength(0)
  })
})
