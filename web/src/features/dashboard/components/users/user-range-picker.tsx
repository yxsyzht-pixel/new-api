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
import { useTranslation } from 'react-i18next'

import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { TIME_RANGE_PRESETS } from '@/features/dashboard/constants'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'

interface UserRangePickerProps {
  /** Rolling window in days, used whenever no custom window is set. */
  selectedRange: number
  customStart?: number
  customEnd?: number
  onPresetChange: (days: number) => void
  onCustomChange: (start?: number, end?: number) => void
}

/**
 * The quick presets plus a window chosen by hand.
 *
 * The two are one control rather than two: a custom window and a rolling one
 * cannot both be in force, and showing a preset as selected while the charts
 * cover some other fortnight is the kind of thing people only notice after
 * they have drawn a conclusion from it. So while a custom window is set, no
 * preset is highlighted, and picking one clears the window.
 *
 * The picker itself is the one the logs page uses. Two different date fields
 * in one product is a small tax on everyone who learns the first one.
 */
export function UserRangePicker(props: UserRangePickerProps) {
  const { t } = useTranslation()

  const hasCustom = props.customStart != null && props.customEnd != null

  return (
    <div className='flex shrink-0 items-center gap-1.5'>
      <Tabs
        value={hasCustom ? '' : String(props.selectedRange)}
        onValueChange={(value) => value && props.onPresetChange(Number(value))}
      >
        <TabsList>
          {TIME_RANGE_PRESETS.map((preset) => (
            <TabsTrigger
              key={preset.days}
              value={String(preset.days)}
              className='px-2.5 text-xs'
            >
              {t(preset.label)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

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
