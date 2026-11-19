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
import { Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { getKeyChannelOptions } from '@/features/keys/api'

import {
  ASSET_FORMAT_BYTEPLUS,
  ASSET_FORMAT_NEWAPI,
  ASSET_FORMAT_VOLCENGINE,
  assetOutboundAddress,
  parseAssetOutbounds,
  type AssetOutbound,
} from './asset-library-config'
import { AssetOutboundDialog } from './asset-outbound-dialog'

type AssetOutboundsEditorProps = {
  value: string
  onChange: (value: string) => void
  customFormatIds: string[]
}

export function AssetOutboundsEditor(props: AssetOutboundsEditorProps) {
  const { t } = useTranslation()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editData, setEditData] = useState<AssetOutbound | null>(null)
  const outbounds = useMemo(
    () => parseAssetOutbounds(props.value),
    [props.value]
  )

  const { data: channels } = useQuery({
    queryKey: ['key-channel-options'],
    queryFn: getKeyChannelOptions,
    staleTime: 30_000,
  })
  const channelOptions = useMemo(
    () =>
      (channels ?? []).map((channel) => ({
        value: String(channel.id),
        label: `#${channel.id} ${channel.name}`,
        hint:
          channel.status === 1
            ? channel.group
            : `${channel.group} · ${t('Disabled')}`,
      })),
    [channels, t]
  )

  const formatLabel = (format: string) => {
    if (format === ASSET_FORMAT_BYTEPLUS) {
      return t('BytePlus ModelArk (international)')
    }
    if (format === ASSET_FORMAT_NEWAPI) return t('Another New API gateway')
    if (!format || format === ASSET_FORMAT_VOLCENGINE) {
      return t('Volcengine Ark (mainland China)')
    }
    return t('Custom format: {{id}}', { id: format })
  }

  const write = (next: AssetOutbound[]) =>
    props.onChange(JSON.stringify(next, null, 2))

  const handleSave = (outbound: AssetOutbound) => {
    const index = outbounds.findIndex((item) => item.id === outbound.id)
    if (index === -1) {
      write([...outbounds, outbound])
      return
    }
    write(outbounds.map((item, i) => (i === index ? outbound : item)))
  }

  const openDialog = (outbound: AssetOutbound | null) => {
    setEditData(outbound)
    setDialogOpen(true)
  }

  return (
    <div className='space-y-3'>
      <div className='flex justify-end'>
        <Button
          type='button'
          variant='outline'
          onClick={() => openDialog(null)}
        >
          <Plus className='size-4' />
          {t('Add outbound')}
        </Button>
      </div>

      {outbounds.length === 0 ? (
        <div className='text-muted-foreground rounded-lg border border-dashed p-8 text-center text-sm'>
          {t(
            'No outbound yet. Add the vendor account that holds your assets to switch the asset library on.'
          )}
        </div>
      ) : (
        <StaticDataTable
          data={outbounds}
          getRowKey={(outbound) => outbound.id}
          columns={[
            {
              id: 'outbound',
              header: t('Outbound'),
              cell: (outbound) => (
                <div className='space-y-1'>
                  <div className='font-medium'>
                    {outbound.name || outbound.id}
                  </div>
                  <code className='bg-muted rounded px-1.5 py-0.5 text-xs'>
                    {outbound.id}
                  </code>
                </div>
              ),
            },
            {
              id: 'upstream',
              header: t('Upstream'),
              cell: (outbound) => (
                <div className='space-y-1'>
                  <div>{formatLabel(outbound.format)}</div>
                  <div className='text-muted-foreground text-xs break-all'>
                    {assetOutboundAddress(outbound)}
                  </div>
                </div>
              ),
            },
            {
              id: 'channels',
              header: t('Channels'),
              cell: (outbound) =>
                outbound.channel_ids?.length
                  ? outbound.channel_ids.map((id) => `#${id}`).join(', ')
                  : t('Every channel'),
            },
            {
              id: 'status',
              header: t('Status'),
              cell: (outbound) => (
                <StatusBadge
                  variant={outbound.disabled ? 'neutral' : 'success'}
                  label={outbound.disabled ? t('Disabled') : t('Enabled')}
                  copyable={false}
                />
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (outbound) => (
                <StaticRowActions
                  editLabel={t('Edit')}
                  deleteLabel={t('Delete')}
                  menuLabel={t('Open menu')}
                  onEdit={() => openDialog(outbound)}
                  onDelete={() =>
                    write(outbounds.filter((item) => item.id !== outbound.id))
                  }
                />
              ),
            },
          ]}
        />
      )}

      <AssetOutboundDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onSave={handleSave}
        editData={editData}
        existingIds={outbounds.map((outbound) => outbound.id)}
        customFormatIds={props.customFormatIds}
        channelOptions={channelOptions}
      />
    </div>
  )
}
