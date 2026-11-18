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
import { useMemo } from 'react'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import {
  ASSET_PRICED_ACTIONS,
  normalizeActionPrices,
} from './asset-library-config'

type AssetActionPricesEditorProps = {
  value: string
  onChange: (value: string) => void
}

/** One quota amount per asset operation; an empty box is free. */
export function AssetActionPricesEditor(props: AssetActionPricesEditorProps) {
  const prices = useMemo(
    () => normalizeActionPrices(props.value),
    [props.value]
  )

  const setPrice = (action: string, text: string) => {
    const next = { ...prices }
    const amount = Math.trunc(Number(text))
    if (text.trim() !== '' && Number.isFinite(amount) && amount > 0) {
      next[action] = amount
    } else {
      delete next[action]
    }
    props.onChange(JSON.stringify(next))
  }

  return (
    <div className='grid gap-x-5 gap-y-3 sm:grid-cols-2'>
      {ASSET_PRICED_ACTIONS.map((action) => (
        <div key={action} className='flex items-center justify-between gap-3'>
          <Label
            htmlFor={`asset-price-${action}`}
            className='min-w-0 font-mono text-xs break-all'
          >
            {action}
          </Label>
          <Input
            id={`asset-price-${action}`}
            type='number'
            min={0}
            step={1}
            placeholder='0'
            className='w-28 shrink-0'
            value={prices[action] ?? ''}
            onChange={(event) => setPrice(action, event.target.value)}
          />
        </div>
      ))}
    </div>
  )
}
