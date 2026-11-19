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
import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

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
import { Textarea } from '@/components/ui/textarea'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { AssetActionPricesEditor } from './asset-action-prices-editor'
import {
  ASSET_MEDIA_STORAGE_DEFAULT_MAX_FILE_MB,
  ASSET_MEDIA_STORAGE_DEFAULT_PREFIX,
  ASSET_ONE_CALL_DEFAULT_QUEUE_WAIT_SECONDS,
  ASSET_ONE_CALL_DEFAULT_REUSE_HOURS,
  ASSET_ONE_CALL_DEFAULT_WAIT_SECONDS,
  assetLibraryFormValues,
  buildAssetLibraryConfig,
  customAssetFormatIds,
  parseAssetLibraryConfig,
  parseAssetOutbounds,
} from './asset-library-config'
import { AssetOutboundsEditor } from './asset-outbounds-editor'

/** Stands for "no default": the first usable outbound serves. */
const FIRST_OUTBOUND = '__first__'

function isJsonArray(text: string): boolean {
  try {
    return Array.isArray(JSON.parse(text))
  } catch {
    return false
  }
}

const assetLibrarySchema = z.object({
  outbounds: z.string(),
  defaultOutbound: z.string(),
  selectorHeader: z.string(),
  failover: z.boolean(),
  oneCallEnabled: z.boolean(),
  autoRegister: z.boolean(),
  waitSeconds: z.number().int().min(0),
  reuseHours: z.number().int(),
  deleteAfterUse: z.boolean(),
  maxAssets: z.number().int().min(0),
  uploadsPerMinute: z.number().int().min(0),
  queueWaitSeconds: z.number().int().min(0),
  storageEnabled: z.boolean(),
  storageAccessKey: z.string(),
  storageSecretKey: z.string(),
  storageBucket: z.string(),
  storageDomain: z.string(),
  storagePrefix: z.string(),
  storagePrivate: z.boolean(),
  storageMaxFileMb: z.number().int().min(0),
  rateLimitCount: z.number().int().min(0),
  rateLimitDurationSeconds: z.number().int().min(0),
  actionPrices: z.string(),
  customFormats: z
    .string()
    .refine(isJsonArray, 'Custom formats must be a JSON array'),
})

type AssetLibraryValues = z.infer<typeof assetLibrarySchema>

type AssetLibrarySectionProps = {
  /** The VolcAssetConfig option as the server sends it, secrets left out. */
  config: string
}

function SubsectionHeading(props: { title: string; description?: string }) {
  return (
    <div data-settings-form-span='full' className='space-y-1 pt-2'>
      <h4 className='text-sm font-medium'>{props.title}</h4>
      {props.description ? (
        <p className='text-muted-foreground text-xs'>{props.description}</p>
      ) : null}
    </div>
  )
}

export function AssetLibrarySection(props: AssetLibrarySectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [stored, setStored] = useState(() =>
    parseAssetLibraryConfig(props.config)
  )
  const form = useForm<AssetLibraryValues>({
    resolver: zodResolver(assetLibrarySchema),
    defaultValues: assetLibraryFormValues(stored),
  })
  const { isDirty, isSubmitting } = form.formState

  const outboundsJson = form.watch('outbounds')
  const customFormatsJson = form.watch('customFormats')
  const oneCallEnabled = form.watch('oneCallEnabled')
  const storageEnabled = form.watch('storageEnabled')
  const outboundIds = useMemo(
    () => parseAssetOutbounds(outboundsJson).map((outbound) => outbound.id),
    [outboundsJson]
  )
  const customFormatIds = useMemo(
    () => customAssetFormatIds(customFormatsJson),
    [customFormatsJson]
  )
  const defaultOutboundItems = [
    { value: FIRST_OUTBOUND, label: t('The first usable outbound') },
    ...outboundIds.map((id) => ({ value: id, label: id })),
  ]

  async function onSubmit(values: AssetLibraryValues) {
    const next = buildAssetLibraryConfig(values, stored)
    await updateOption.mutateAsync({
      key: 'VolcAssetConfig',
      value: JSON.stringify(next),
    })
    // Secrets are stored on the server and never come back; the boxes start
    // empty again and an empty box keeps what is stored.
    const saved = {
      ...next,
      outbounds: next.outbounds.map((outbound) => ({
        ...outbound,
        secret_key: '',
        access_token: '',
      })),
      media_storage: { ...next.media_storage, secret_key: '' },
    }
    setStored(saved)
    form.reset(assetLibraryFormValues(saved))
  }

  return (
    <SettingsSection title={t('Asset library')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel={t('Save asset library settings')}
          />

          <p
            data-settings-form-span='full'
            className='text-muted-foreground text-sm'
          >
            {t(
              'Seedance 2 only takes a reference that shows a person when it is an asset of the vendor’s asset library. Users manage their own assets at POST /volc-asset/{Action} with their API key; every user gets a group of their own.'
            )}
          </p>

          <SubsectionHeading
            title={t('Outbounds')}
            description={t(
              'Each outbound is a vendor account and project. Assets only work on channels whose key belongs to the same account and project.'
            )}
          />
          <FormField
            control={form.control}
            name='outbounds'
            render={({ field }) => (
              <FormItem data-settings-form-span='full'>
                <FormControl>
                  <AssetOutboundsEditor
                    value={field.value}
                    onChange={field.onChange}
                    customFormatIds={customFormatIds}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='defaultOutbound'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Default outbound')}</FormLabel>
                <Select
                  items={defaultOutboundItems}
                  onValueChange={(value) =>
                    field.onChange(value === FIRST_OUTBOUND ? '' : value)
                  }
                  value={
                    field.value && outboundIds.includes(field.value)
                      ? field.value
                      : FIRST_OUTBOUND
                  }
                >
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {defaultOutboundItems.map((item) => (
                        <SelectItem key={item.value} value={item.value}>
                          {item.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t('Used when a request names no outbound.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='selectorHeader'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Outbound selector header')}</FormLabel>
                <FormControl>
                  <Input placeholder='X-Asset-Outbound' {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'The request header a caller names an outbound ID in. X-Asset-Outbound when empty.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='failover'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Fall back to other outbounds')}</FormLabel>
                  <FormDescription>
                    {t(
                      'When the chosen outbound is not usable, the next enabled one serves the request.'
                    )}
                  </FormDescription>
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

          <SubsectionHeading
            title={t('Portraits in generation requests')}
            description={t(
              'A generation request can carry ordinary URLs and mark which of them show a person: "portrait": true for an AI-generated person, or "portrait": "<asset group ID>" for a real person who passed the liveness check. The mark goes on one reference or on the whole request, and in metadata for /v1/videos. The gateway registers the file, waits for the library to accept it and sends its asset ID.'
            )}
          />
          <FormField
            control={form.control}
            name='oneCallEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Register marked portraits automatically')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'When off, a request that marks a portrait is refused and callers register assets themselves.'
                    )}
                  </FormDescription>
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
          <FormField
            control={form.control}
            name='autoRegister'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Treat every Seedance 2 reference as a portrait')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Callers then need no mark: every image and video reference of a Seedance 2 request is registered as an AI-generated person. References that show nobody go through the library’s review too, which takes longer and can refuse them; a request can still say "portrait": false.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={!oneCallEnabled}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />
          <FormField
            control={form.control}
            name='waitSeconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Longest wait for the library (seconds)')}
                </FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    placeholder={String(ASSET_ONE_CALL_DEFAULT_WAIT_SECONDS)}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'The vendor promises no processing time. A request that runs out of this wait is refused with asset_processing; sending it again finds the same asset. 0 uses {{seconds}} seconds.',
                    { seconds: ASSET_ONE_CALL_DEFAULT_WAIT_SECONDS }
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='reuseHours'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Reuse a registered URL for (hours)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    step={1}
                    placeholder={String(ASSET_ONE_CALL_DEFAULT_REUSE_HOURS)}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'The same URL from the same user answers with the asset already made for it. 0 uses {{hours}} hours; a negative number registers every time.',
                    { hours: ASSET_ONE_CALL_DEFAULT_REUSE_HOURS }
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <SubsectionHeading
            title={t('Room in the asset library')}
            description={t(
              'A vendor account holds a limited number of assets and takes a limited number of uploads a minute. The limits below apply to each outbound on its own.'
            )}
          />
          <FormField
            control={form.control}
            name='deleteAfterUse'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Delete assets once their tasks finish')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'An asset the gateway registered for a generation request is deleted from the library as soon as no unfinished task uses it. Assets users register themselves are never deleted.'
                    )}
                  </FormDescription>
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
          <FormField
            control={form.control}
            name='maxAssets'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Assets the library holds')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'A request that needs room waits in line until an asset is deleted. 0 means no limit.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='uploadsPerMinute'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Uploads per minute')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'How many assets one outbound registers a minute; further uploads wait. 0 means no limit.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='queueWaitSeconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Longest wait in line (seconds)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    placeholder={String(
                      ASSET_ONE_CALL_DEFAULT_QUEUE_WAIT_SECONDS
                    )}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'A request that waits longer is refused with asset_library_full or asset_upload_rate_limited and can be sent again. 0 uses {{seconds}} seconds.',
                    { seconds: ASSET_ONE_CALL_DEFAULT_QUEUE_WAIT_SECONDS }
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <SubsectionHeading
            title={t('Files sent with a request')}
            description={t(
              'A request can carry a reference as a base64 data URL, or as an uploaded file on /v1/videos. The gateway uploads the file to the storage below and sends the vendor its URL.'
            )}
          />
          <FormField
            control={form.control}
            name='storageEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Store request files in Qiniu')}</FormLabel>
                  <FormDescription>
                    {t(
                      'When off, an inline image goes to the vendor as it is and an inline portrait is refused.'
                    )}
                  </FormDescription>
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
          <FormField
            control={form.control}
            name='storageAccessKey'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Qiniu Access Key')}</FormLabel>
                <FormControl>
                  <Input
                    autoComplete='off'
                    disabled={!storageEnabled}
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='storageSecretKey'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Qiniu Secret Key')}</FormLabel>
                <FormControl>
                  <Input
                    type='password'
                    autoComplete='new-password'
                    disabled={!storageEnabled}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Stored on the server and never shown. Leave empty to keep the stored key.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='storageBucket'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Qiniu bucket')}</FormLabel>
                <FormControl>
                  <Input disabled={!storageEnabled} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='storageDomain'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Bucket domain')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder='https://cdn.example.com'
                    disabled={!storageEnabled}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'The address the bucket is served at. The vendor fetches the files from here, so it has to be reachable from the internet.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='storagePrefix'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Key prefix')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={ASSET_MEDIA_STORAGE_DEFAULT_PREFIX}
                    disabled={!storageEnabled}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Files are stored under this path and named by their content, so the same file is stored once. {{prefix}} when empty.',
                    { prefix: ASSET_MEDIA_STORAGE_DEFAULT_PREFIX }
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='storageMaxFileMb'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Largest file (MB)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    placeholder={String(
                      ASSET_MEDIA_STORAGE_DEFAULT_MAX_FILE_MB
                    )}
                    disabled={!storageEnabled}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t('0 uses {{size}} MB.', {
                    size: ASSET_MEDIA_STORAGE_DEFAULT_MAX_FILE_MB,
                  })}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='storagePrivate'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Private bucket')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Sign every file URL, so that a bucket that is not public can be read.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={!storageEnabled}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <SubsectionHeading
            title={t('Charges and limits')}
            description={t(
              'A fixed charge in quota units for each successful asset operation; empty is free. Portraits registered by a generation request are charged as CreateAsset.'
            )}
          />
          <FormField
            control={form.control}
            name='actionPrices'
            render={({ field }) => (
              <FormItem data-settings-form-span='full'>
                <FormControl>
                  <AssetActionPricesEditor
                    value={field.value}
                    onChange={field.onChange}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='rateLimitCount'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Asset requests per user')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t('0 in either box means no limit.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='rateLimitDurationSeconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Within (seconds)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <SubsectionHeading title={t('Advanced')} />
          <FormField
            control={form.control}
            name='customFormats'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Custom outbound formats')}</FormLabel>
                <FormControl>
                  <Textarea
                    rows={6}
                    className='font-mono text-xs'
                    spellCheck={false}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'A JSON array of request and response templates for upstreams that speak neither the vendor protocol nor this gateway’s. An outbound uses one by its id.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
