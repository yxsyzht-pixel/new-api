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
import type {
  UserAnalyticsDimension,
  UserAnalyticsMetric,
} from '@/features/dashboard/types'

interface UserMetricTabsProps {
  value: UserAnalyticsMetric
  onValueChange: (value: UserAnalyticsMetric) => void
}

interface UserDimensionTabsProps {
  value: UserAnalyticsDimension
  onValueChange: (value: UserAnalyticsDimension) => void
}

/** Switches the consumption charts between per-account and per-key totals. */
export function UserDimensionTabs(props: UserDimensionTabsProps) {
  const { t } = useTranslation()

  return (
    <Tabs
      value={props.value}
      onValueChange={(value) =>
        props.onValueChange(value as UserAnalyticsDimension)
      }
      className='shrink-0'
    >
      <TabsList aria-label={t('Group by')}>
        <span className='text-muted-foreground px-2 text-xs font-medium whitespace-nowrap'>
          {t('Group by')}
        </span>
        <TabsTrigger value='user' className='px-2.5 text-xs'>
          {t('User')}
        </TabsTrigger>
        <TabsTrigger value='token' className='px-2.5 text-xs'>
          {t('Key')}
        </TabsTrigger>
      </TabsList>
    </Tabs>
  )
}

export function UserMetricTabs(props: UserMetricTabsProps) {
  const { t } = useTranslation()

  return (
    <Tabs
      value={props.value}
      onValueChange={(value) =>
        props.onValueChange(value as UserAnalyticsMetric)
      }
      className='shrink-0'
    >
      <TabsList aria-label={t('Type')}>
        <span className='text-muted-foreground px-2 text-xs font-medium whitespace-nowrap'>
          {t('Type')}
        </span>
        <TabsTrigger value='quota' className='px-2.5 text-xs'>
          {t('Amount')}
        </TabsTrigger>
        <TabsTrigger value='tokens' className='px-2.5 text-xs'>
          {t('Tokens')}
        </TabsTrigger>
      </TabsList>
    </Tabs>
  )
}
