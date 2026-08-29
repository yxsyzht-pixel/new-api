/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi } from 'vitest'

import { UserRangePicker } from '../user-range-picker'

const noop = () => {}

describe('UserRangePicker', () => {
  // A preset shown as selected while the charts cover some other fortnight is
  // the kind of thing people only notice after drawing a conclusion from it.
  it('highlights no preset while a hand-picked window is in force', () => {
    const { rerender } = render(
      <UserRangePicker
        selectedRange={7}
        onPresetChange={noop}
        onCustomChange={noop}
      />
    )
    expect(screen.getByRole('tab', { name: '7 Days' })).toHaveAttribute(
      'aria-selected',
      'true'
    )

    rerender(
      <UserRangePicker
        selectedRange={7}
        customStart={1_700_000_000}
        customEnd={1_700_600_000}
        onPresetChange={noop}
        onCustomChange={noop}
      />
    )
    expect(screen.getByRole('tab', { name: '7 Days' })).toHaveAttribute(
      'aria-selected',
      'false'
    )
  })

  // Picking a preset is the way back out of a custom window, so it has to
  // clear one rather than leave both applied.
  it('reports a preset choice so the caller can drop the custom window', async () => {
    const onPresetChange = vi.fn()
    render(
      <UserRangePicker
        selectedRange={7}
        customStart={1_700_000_000}
        customEnd={1_700_600_000}
        onPresetChange={onPresetChange}
        onCustomChange={noop}
      />
    )

    await userEvent.click(screen.getByRole('tab', { name: '14 Days' }))
    expect(onPresetChange).toHaveBeenCalledWith(14)
  })
})
