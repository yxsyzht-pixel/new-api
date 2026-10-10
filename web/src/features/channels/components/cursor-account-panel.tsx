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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, LogIn, LogOut, RefreshCw } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import {
  getCursorAccount,
  logoutCursor,
  startCursorLogin,
  type CursorAccount,
} from '../api'

type CursorAccountPanelProps = {
  /** A channel still being created has no bridge to ask yet. */
  channelId?: number
  /** Without sensitive-write permission the panel only shows the account. */
  disabled?: boolean
}

/**
 * Signs a Cursor channel's bridge in to a Cursor account. The account lives in
 * the bridge, not in the channel key: the bridge prints a cursor.com link, the
 * operator opens it in their own browser, and the bridge stores the login once
 * Cursor confirms — this panel just polls until then.
 */
export function CursorAccountPanel({
  channelId,
  disabled = false,
}: CursorAccountPanelProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirmLogout, setConfirmLogout] = useState(false)
  const queryKey = ['channels', channelId, 'cursor-account']

  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getCursorAccount(channelId as number, signal),
    enabled: Boolean(channelId),
    refetchInterval: (current) =>
      current.state.data?.login?.status === 'pending' ? 3000 : false,
    refetchOnWindowFocus: false,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })
  const account = query.data
  const login = account?.login
  const pending = login?.status === 'pending'

  // Announce only a login this panel watched finish, not one found finished.
  const lastStatus = useRef(login?.status)
  useEffect(() => {
    const previous = lastStatus.current
    lastStatus.current = login?.status
    if (previous !== 'pending') return
    if (login?.status === 'succeeded' && account?.authenticated) {
      toast.success(
        t('Signed in to Cursor as {{email}}', { email: account.email ?? '' })
      )
    } else if (login?.status === 'failed') {
      toast.error(t('Cursor sign-in did not finish'))
    }
  }, [account?.authenticated, account?.email, login?.status, t])

  const startLogin = useMutation({
    mutationFn: (restart: boolean) =>
      startCursorLogin(channelId as number, restart),
    onSuccess: (state) => {
      queryClient.setQueryData<CursorAccount>(queryKey, (previous) => ({
        authenticated: previous?.authenticated ?? false,
        email: previous?.email,
        subscription_tier: previous?.subscription_tier,
        login: state,
      }))
      lastStatus.current = state.status
      if (state.url) {
        window.open(state.url, '_blank', 'noopener,noreferrer')
      }
    },
    onError: (error) => {
      toast.error(getServerErrorMessage(error, t('Failed to start sign-in')))
    },
  })

  const logout = useMutation({
    mutationFn: () => logoutCursor(channelId as number),
    onSuccess: (next) => {
      queryClient.setQueryData<CursorAccount>(queryKey, next)
      setConfirmLogout(false)
      toast.success(t('Signed out of Cursor'))
    },
    onError: (error) => {
      toast.error(getServerErrorMessage(error, t('Sign-out failed')))
    },
  })

  if (!channelId) {
    return (
      <div className='border-border/60 text-muted-foreground border-y py-4 text-xs'>
        {t('Save the channel first, then sign in to Cursor here.')}
      </div>
    )
  }

  let startLabel = t('Sign in to Cursor')
  if (account?.authenticated) {
    startLabel = t('Switch account')
  } else if (pending) {
    startLabel = t('Get a new link')
  }

  let status
  if (query.isPending) {
    status = (
      <span className='text-muted-foreground text-xs'>
        {t('Checking the Cursor sign-in...')}
      </span>
    )
  } else if (query.isError) {
    status = (
      <span className='text-destructive text-xs'>
        {getServerErrorMessage(query.error, t('Cannot reach the Cursor bridge'))}
      </span>
    )
  } else if (account?.authenticated) {
    status = (
      <span className='flex min-w-0 flex-wrap items-center gap-2'>
        <StatusBadge
          label={t('Signed in')}
          variant='success'
          size='sm'
          copyable={false}
        />
        <span className='truncate font-medium'>{account.email}</span>
        {account.subscription_tier && (
          <span className='text-muted-foreground text-xs'>
            {t('Plan: {{plan}}', { plan: account.subscription_tier })}
          </span>
        )}
      </span>
    )
  } else {
    status = (
      <span className='flex flex-wrap items-center gap-2'>
        <StatusBadge
          label={t('Not signed in')}
          variant='warning'
          size='sm'
          copyable={false}
        />
        {account?.error && (
          <span className='text-muted-foreground text-xs'>{account.error}</span>
        )}
      </span>
    )
  }

  return (
    <div className='border-border/60 flex flex-col gap-3 border-y py-4'>
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
        <div className='flex min-w-0 flex-col gap-1 text-sm'>{status}</div>
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() =>
              startLogin.mutate(Boolean(account?.authenticated) || pending)
            }
            disabled={disabled || startLogin.isPending || query.isError}
          >
            {startLogin.isPending ? (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' />
            ) : (
              <LogIn className='mr-2 h-4 w-4' />
            )}
            {startLabel}
          </Button>
          {account?.authenticated && (
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => setConfirmLogout(true)}
              disabled={disabled || logout.isPending}
            >
              <LogOut className='mr-2 h-4 w-4' />
              {t('Sign out')}
            </Button>
          )}
          <Button
            type='button'
            variant='ghost'
            size='sm'
            aria-label={t('Refresh')}
            onClick={() => void query.refetch()}
            disabled={query.isFetching}
          >
            <RefreshCw
              className={cn('h-4 w-4', query.isFetching && 'animate-spin')}
            />
          </Button>
        </div>
      </div>

      {pending && login?.url && (
        <Alert>
          <AlertDescription className='flex flex-col gap-2'>
            <span>
              {t(
                'Open this link in your browser and sign in with the Cursor account whose subscription this channel should use. This panel updates by itself once Cursor confirms; the link stays valid for about 20 minutes.'
              )}
            </span>
            <span className='flex flex-wrap items-center gap-2'>
              <a
                href={login.url}
                target='_blank'
                rel='noopener noreferrer'
                className='text-primary underline underline-offset-2'
              >
                {t('Open the Cursor sign-in page')}
              </a>
              <CopyButton
                value={login.url}
                variant='outline'
                size='sm'
                tooltip={t('Copy link')}
              />
            </span>
          </AlertDescription>
        </Alert>
      )}
      {pending && !login?.url && (
        <span className='text-muted-foreground text-xs'>
          {t('Waiting for the sign-in link...')}
        </span>
      )}
      {login?.status === 'failed' && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Cursor sign-in did not finish: {{error}}', {
              error: login.error ?? '',
            })}
          </AlertDescription>
        </Alert>
      )}

      <Alert className='border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-50'>
        <AlertDescription>
          {t(
            'Every request on this channel spends the signed-in Cursor plan. Sharing one subscription with many users may breach Cursor’s terms and get the account restricted.'
          )}
        </AlertDescription>
      </Alert>

      <ConfirmDialog
        open={confirmLogout}
        onOpenChange={setConfirmLogout}
        title={t('Sign out of Cursor?')}
        desc={t(
          'Requests on this channel will fail until someone signs in again.'
        )}
        confirmText={t('Sign out')}
        destructive
        isLoading={logout.isPending}
        handleConfirm={() => logout.mutate()}
      />
    </div>
  )
}
