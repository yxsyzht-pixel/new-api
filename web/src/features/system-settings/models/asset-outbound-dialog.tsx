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
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { Dialog } from '@/components/dialog'
import { MultiSelect } from '@/components/multi-select'
import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import {
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import {
  ASSET_FORMAT_BYTEPLUS,
  ASSET_FORMAT_NEWAPI,
  ASSET_FORMAT_VOLCENGINE,
  type AssetOutbound,
} from './asset-library-config'

const assetOutboundSchema = z.object({
  id: z
    .string()
    .trim()
    .min(1, 'Outbound ID is required')
    .regex(
      /^[A-Za-z0-9._-]+$/,
      'Use letters, digits, dots, dashes and underscores only'
    ),
  name: z.string(),
  format: z.string().min(1),
  region: z.string(),
  baseUrl: z.string(),
  projectName: z.string(),
  groupType: z.string(),
  accessKey: z.string(),
  secretKey: z.string(),
  accessToken: z.string(),
  channelIds: z.array(z.string()),
  enabled: z.boolean(),
})

type AssetOutboundFormValues = z.infer<typeof assetOutboundSchema>

const ASSET_OUTBOUND_FORM_ID = 'asset-outbound-form'

const EMPTY_VALUES: AssetOutboundFormValues = {
  id: '',
  name: '',
  format: ASSET_FORMAT_BYTEPLUS,
  region: '',
  baseUrl: '',
  projectName: 'default',
  groupType: '',
  accessKey: '',
  secretKey: '',
  accessToken: '',
  channelIds: [],
  enabled: true,
}

function toFormValues(outbound: AssetOutbound): AssetOutboundFormValues {
  return {
    id: outbound.id,
    name: outbound.name ?? '',
    format: outbound.format || ASSET_FORMAT_VOLCENGINE,
    region: outbound.region ?? '',
    baseUrl: outbound.base_url ?? '',
    projectName: outbound.project_name ?? '',
    groupType: outbound.group_type ?? '',
    accessKey: outbound.access_key ?? '',
    secretKey: outbound.secret_key ?? '',
    accessToken: outbound.access_token ?? '',
    channelIds: (outbound.channel_ids ?? []).map(String),
    enabled: !outbound.disabled,
  }
}

function toOutbound(
  values: AssetOutboundFormValues,
  previous: AssetOutbound | null
): AssetOutbound {
  return {
    ...previous,
    id: values.id.trim(),
    name: values.name.trim(),
    format: values.format,
    region: values.region.trim(),
    base_url: values.baseUrl.trim(),
    project_name: values.projectName.trim(),
    group_type: values.groupType.trim(),
    access_key: values.accessKey.trim(),
    secret_key: values.secretKey,
    access_token: values.accessToken,
    channel_ids: values.channelIds.map(Number),
    disabled: !values.enabled,
  }
}

export type AssetChannelOption = { value: string; label: string; hint?: string }

type AssetOutboundDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: (outbound: AssetOutbound) => void
  editData: AssetOutbound | null
  existingIds: string[]
  customFormatIds: string[]
  channelOptions: AssetChannelOption[]
}

export function AssetOutboundDialog(props: AssetOutboundDialogProps) {
  const { t } = useTranslation()
  const isEditMode = props.editData !== null
  const form = useForm<AssetOutboundFormValues>({
    resolver: zodResolver(assetOutboundSchema),
    defaultValues: EMPTY_VALUES,
  })
  const editData = props.editData

  useEffect(() => {
    form.reset(editData ? toFormValues(editData) : EMPTY_VALUES)
  }, [editData, form, props.open])

  const format = form.watch('format')
  const signed =
    format === ASSET_FORMAT_VOLCENGINE || format === ASSET_FORMAT_BYTEPLUS
  const gateway = format === ASSET_FORMAT_NEWAPI
  const custom = !signed && !gateway

  const formatItems = [
    { value: ASSET_FORMAT_BYTEPLUS, label: t('BytePlus ModelArk') },
    { value: ASSET_FORMAT_VOLCENGINE, label: t('Volcengine Ark') },
    { value: ASSET_FORMAT_NEWAPI, label: t('Another New API gateway') },
    ...props.customFormatIds.map((id) => ({
      value: id,
      label: t('Custom format: {{id}}', { id }),
    })),
  ]

  const handleSubmit = (values: AssetOutboundFormValues) => {
    const id = values.id.trim()
    if (!isEditMode && props.existingIds.includes(id)) {
      form.setError('id', {
        message: 'An outbound with this ID already exists',
      })
      return
    }
    if (gateway && values.baseUrl.trim() === '') {
      form.setError('baseUrl', { message: 'Base URL is required' })
      return
    }
    props.onSave(toOutbound(values, editData))
    props.onOpenChange(false)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={isEditMode ? t('Edit outbound') : t('Add outbound')}
      description={t(
        'An outbound is one vendor account and project that holds assets.'
      )}
      contentClassName='sm:max-w-[560px]'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={ASSET_OUTBOUND_FORM_ID}>
            {isEditMode ? t('Update') : t('Add')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id={ASSET_OUTBOUND_FORM_ID}
          onSubmit={(event) => {
            // The dialog sits inside the settings form; its submit stays here.
            event.stopPropagation()
            void form.handleSubmit(handleSubmit)(event)
          }}
          className='space-y-4'
          autoComplete='off'
        >
          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='id'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Outbound ID')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder='byteplus-main'
                      disabled={isEditMode}
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Name')}</FormLabel>
                  <FormControl>
                    <Input {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='format'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Upstream')}</FormLabel>
                <Select
                  items={formatItems}
                  onValueChange={field.onChange}
                  value={field.value}
                >
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {formatItems.map((item) => (
                        <SelectItem key={item.value} value={item.value}>
                          {item.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />

          {(gateway || custom) && (
            <FormField
              control={form.control}
              name='baseUrl'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Base URL')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder='https://gateway.example.com/volc-asset'
                      {...field}
                    />
                  </FormControl>
                  {gateway && (
                    <FormDescription>
                      {t(
                        'The address of the other gateway’s asset endpoints, up to and including /volc-asset.'
                      )}
                    </FormDescription>
                  )}
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

          {(signed || custom) && (
            <>
              <FormField
                control={form.control}
                name='region'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Region')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={
                          format === ASSET_FORMAT_BYTEPLUS
                            ? 'ap-southeast-1'
                            : 'cn-beijing'
                        }
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <div className='grid gap-4 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='accessKey'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Access Key</FormLabel>
                      <FormControl>
                        <Input autoComplete='off' {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='secretKey'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Secret Key</FormLabel>
                      <FormControl>
                        <PasswordInput autoComplete='new-password' {...field} />
                      </FormControl>
                      {isEditMode && (
                        <FormDescription>
                          {t('Leave empty to keep the stored one.')}
                        </FormDescription>
                      )}
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </>
          )}

          {(gateway || custom) && (
            <FormField
              control={form.control}
              name='accessToken'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Access token')}</FormLabel>
                  <FormControl>
                    <PasswordInput autoComplete='new-password' {...field} />
                  </FormControl>
                  {isEditMode && (
                    <FormDescription>
                      {t('Leave empty to keep the stored one.')}
                    </FormDescription>
                  )}
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='projectName'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Project name')}</FormLabel>
                  <FormControl>
                    <Input placeholder='default' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Assets are only usable by channels whose key is in the same project.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='groupType'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Asset group type')}</FormLabel>
                  <FormControl>
                    <Input placeholder='AIGC' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t('The type of each user’s own group. AIGC when empty.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='channelIds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Channels of this account')}</FormLabel>
                <FormControl>
                  <MultiSelect
                    options={props.channelOptions}
                    selected={field.value}
                    onChange={field.onChange}
                    placeholder={t('Select channels (empty for every channel)')}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'A generation request registers its portraits through the outbound of the channel it runs on. Leave empty when every channel uses this account.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enabled')}</FormLabel>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />
        </form>
      </Form>
    </Dialog>
  )
}
