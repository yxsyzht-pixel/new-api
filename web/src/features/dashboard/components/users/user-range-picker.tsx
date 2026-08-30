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
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'

interface UserRangePickerProps {
  customStart?: number
  customEnd?: number
  onCustomChange: (start?: number, end?: number) => void
}

/**
 * The window the charts cover, chosen by hand.
 *
 * It opens on the last 24 hours (see buildDefaultUserChartsFilters) rather
 * than on a set of preset buttons: the presets and the date field were two
 * controls for one thing, and a preset shown as selected while the charts
 * covered some other fortnight is the kind of mismatch people only notice
 * after they have drawn a conclusion from it.
 *
 * The picker itself is the one the logs page uses. Two different date fields
 * in one product is a small tax on everyone who learns the first one.
 */
export function UserRangePicker(props: UserRangePickerProps) {
  return (
    <div className='flex shrink-0 items-center gap-1.5'>
      <CompactDateTimeRangePicker
        className='h-8 w-auto min-w-40 text-xs'
        start={
          props.customStart ? new Date(props.customStart * 1000) : undefined
        }
        end={props.customEnd ? new Date(props.customEnd * 1000) : undefined}
        onChange={({ start, end }) => {
          // Both ends or neither: a half-open window would silently mean
          // "everything since", which is not what someone picking one date
          // is asking for.
          if (!start || !end) {
            props.onCustomChange(undefined, undefined)
            return
          }
          const from = Math.floor(start.getTime() / 1000)
          const to = Math.floor(end.getTime() / 1000)
          // Tolerate the two entered the wrong way round rather than showing an
          // empty chart and leaving the reason to be guessed at.
          props.onCustomChange(Math.min(from, to), Math.max(from, to))
        }}
      />
    </div>
  )
}
