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
import { useQuery } from '@tanstack/react-query'
import { VChart } from '@visactor/react-vchart'
import { Users, KeyRound, Loader2 } from 'lucide-react'
import { useEffect, useMemo, useState, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useTheme } from '@/context/theme-provider'
import {
  getQuotaDataByTokens,
  getSelfQuotaDataByTokens,
  getUserQuotaDataByUsers,
} from '@/features/dashboard/api'
import {
  granularityForWindow,
  processUserChartData,
} from '@/features/dashboard/lib'
import type {
  ProcessedUserChartData,
  UserAnalyticsDimension,
  UserAnalyticsMetric,
  UserChartsFilters,
} from '@/features/dashboard/types'
import { getRollingDateRange } from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'

import { UserDimensionTabs, UserMetricTabs } from './user-metric-tabs'
import { UserRangePicker } from './user-range-picker'
import { UserScopeFilter } from './user-scope-filter'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

const USER_CHARTS: {
  value: string
  labelKey: string
  tokenLabelKey: string
  specKey: keyof ProcessedUserChartData
}[] = [
  {
    value: 'rank',
    labelKey: 'User Consumption Ranking',
    tokenLabelKey: 'Key Consumption Ranking',
    specKey: 'spec_user_rank',
  },
  {
    value: 'trend',
    labelKey: 'User Consumption Trend',
    tokenLabelKey: 'Key Consumption Trend',
    specKey: 'spec_user_trend',
  },
]

const TOP_USER_LIMIT_OPTIONS = [5, 10, 20, 50]

interface UserChartsProps {
  filters: UserChartsFilters
  onFiltersChange: (filters: UserChartsFilters) => void
  /**
   * 'self' restricts the charts to the viewer's own keys, which is the only
   * meaningful breakdown for a non-admin account.
   */
  scope?: 'admin' | 'self'
}

export function UserCharts(props: UserChartsProps) {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)

  // The selection is owned by the dashboard parent so it persists across
  // sub-section switches; the rolling window is derived from the chosen range.
  const topUserLimit = props.filters.topUserLimit
  const metric = props.filters.metric
  const isSelfScope = props.scope === 'self'
  // A single account has nothing to compare across users, so the self view is
  // always broken down by key.
  const dimension = isSelfScope ? 'token' : props.filters.dimension
  const onFiltersChange = props.onFiltersChange

  const customStart = props.filters.customStart
  const customEnd = props.filters.customEnd
  const userFilter = props.filters.userFilter

  const timeRange = useMemo(() => {
    if (customStart != null && customEnd != null) {
      return { start_timestamp: customStart, end_timestamp: customEnd }
    }
    // Clearing the field falls back to the same window the view opens on,
    // rather than to everything ever recorded.
    const { start, end } = getRollingDateRange(1)
    return {
      start_timestamp: Math.floor(start.getTime() / 1000),
      end_timestamp: Math.floor(end.getTime() / 1000),
    }
  }, [customStart, customEnd])

  // Derived rather than chosen: see granularityForWindow.
  const timeGranularity = useMemo(
    () =>
      granularityForWindow(timeRange.start_timestamp, timeRange.end_timestamp),
    [timeRange]
  )

  const handleCustomRangeChange = useCallback(
    (start?: number, end?: number) => {
      onFiltersChange({
        ...props.filters,
        customStart: start,
        customEnd: end,
      })
    },
    [onFiltersChange, props.filters]
  )

  const handleUserFilterChange = useCallback(
    (username?: string) => {
      onFiltersChange({ ...props.filters, userFilter: username })
    },
    [onFiltersChange, props.filters]
  )

  const handleTopUserLimitChange = useCallback(
    (limit: number) => {
      onFiltersChange({ ...props.filters, topUserLimit: limit })
    },
    [onFiltersChange, props.filters]
  )

  const handleMetricChange = useCallback(
    (value: UserAnalyticsMetric) => {
      onFiltersChange({ ...props.filters, metric: value })
    },
    [onFiltersChange, props.filters]
  )

  const handleDimensionChange = useCallback(
    (value: UserAnalyticsDimension) => {
      onFiltersChange({ ...props.filters, dimension: value })
    },
    [onFiltersChange, props.filters]
  )

  useEffect(() => {
    const updateTheme = async () => {
      setThemeReady(false)
      if (!themeManagerPromise) {
        themeManagerPromise = import('@visactor/vchart').then(
          (m) => m.ThemeManager
        )
      }
      const ThemeManager = await themeManagerPromise
      themeManagerRef.current = ThemeManager
      ThemeManager.setCurrentTheme(resolvedTheme === 'dark' ? 'dark' : 'light')
      setThemeReady(true)
    }
    updateTheme()
  }, [resolvedTheme])

  const { data: userData, isLoading } = useQuery({
    queryKey: [
      'dashboard',
      'user-quota',
      props.scope ?? 'admin',
      dimension,
      timeRange,
    ],
    queryFn: () => {
      if (isSelfScope) return getSelfQuotaDataByTokens(timeRange)
      return dimension === 'token'
        ? getQuotaDataByTokens(timeRange)
        : getUserQuotaDataByUsers(timeRange)
    },
    select: (res) => (res.success ? res.data : []),
    staleTime: 60_000,
  })

  // The accounts that actually spent something in this window, heaviest first,
  // so the dropdown opens on the ones someone is likely to be looking for.
  const availableUsers = useMemo(() => {
    if (dimension !== 'token') return []
    const spend = new Map<string, number>()
    for (const item of userData ?? []) {
      if (!item.username) continue
      spend.set(
        item.username,
        (spend.get(item.username) ?? 0) + (item.quota ?? 0)
      )
    }
    return [...spend.entries()]
      .sort((a, b) => b[1] - a[1])
      .map(([username]) => username)
  }, [userData, dimension])

  // Filtering here rather than in the query keeps one fetch serving every
  // account: switching between them is then instant and costs no request.
  const scopedData = useMemo(() => {
    const rows = isLoading ? [] : (userData ?? [])
    if (dimension !== 'token' || !userFilter) return rows
    return rows.filter((item) => item.username === userFilter)
  }, [userData, isLoading, dimension, userFilter])

  const chartData = useMemo(
    () =>
      processUserChartData(
        scopedData,
        timeGranularity,
        t,
        topUserLimit,
        metric,
        dimension
      ),
    [scopedData, timeGranularity, t, topUserLimit, metric, dimension]
  )

  return (
    <div className='space-y-3'>
      <div className='flex items-center gap-1.5 overflow-x-auto pb-1 sm:gap-2'>
        <UserRangePicker
          customStart={customStart}
          customEnd={customEnd}
          onCustomChange={handleCustomRangeChange}
        />

        <Tabs
          value={String(topUserLimit)}
          onValueChange={(value) => handleTopUserLimitChange(Number(value))}
          className='shrink-0'
        >
          <TabsList>
            <span className='text-muted-foreground px-2 text-xs font-medium whitespace-nowrap'>
              {dimension === 'token' ? t('Top Keys') : t('Top Users')}
            </span>
            {TOP_USER_LIMIT_OPTIONS.map((limit) => (
              <TabsTrigger
                key={limit}
                value={String(limit)}
                className='px-2.5 text-xs'
              >
                {t('Top {{count}}', { count: limit })}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

        {!isSelfScope && (
          <UserDimensionTabs
            value={dimension}
            onValueChange={handleDimensionChange}
          />
        )}

        <UserMetricTabs value={metric} onValueChange={handleMetricChange} />

        {!isSelfScope && dimension === 'token' && (
          <UserScopeFilter
            users={availableUsers}
            value={userFilter}
            onValueChange={handleUserFilterChange}
          />
        )}

        {isLoading && (
          <Loader2 className='text-muted-foreground size-4 animate-spin' />
        )}
      </div>

      <div className='grid gap-3'>
        {USER_CHARTS.map((chart) => {
          const spec = chartData[chart.specKey]

          return (
            <div
              key={chart.value}
              className='overflow-hidden rounded-lg border'
            >
              <div className='flex w-full items-center gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
                <IconBadge tone='info' size='sm'>
                  {dimension === 'token' ? <KeyRound /> : <Users />}
                </IconBadge>
                <div className='text-sm font-semibold'>
                  {t(
                    dimension === 'token' ? chart.tokenLabelKey : chart.labelKey
                  )}
                </div>
              </div>

              <div className='h-[300px] p-1.5 sm:h-96 sm:p-2'>
                {isLoading ? (
                  <Skeleton className='h-full w-full' />
                ) : (
                  themeReady &&
                  spec && (
                    <VChart
                      key={`user-${chart.value}-${dimension}-${topUserLimit}-${metric}-${resolvedTheme}`}
                      spec={{
                        ...spec,
                        theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                        background: 'transparent',
                      }}
                      option={VCHART_OPTION}
                    />
                  )
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
