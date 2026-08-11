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
import { type ReactNode, useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ExternalLink, Loader2 } from 'lucide-react'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

/**
 * Sign-in block for channels whose OAuth client only accepts a loopback
 * redirect — Codex and Antigravity both do. Such a redirect can never reach a
 * server-hosted panel, so the flow is: open the consent URL, let the browser
 * land on an address that fails to load, and paste that address back.
 *
 * Both channels drive the identical interaction, so it lives here once rather
 * than twice in the channel drawer.
 */
export type LoopbackOAuthSignInProps = {
  /** Starts the flow; resolves with the URL to open and the state to carry. */
  onStart: () => Promise<{ authUrl: string; state: string }>
  /** Finishes the flow. Rejecting surfaces the error and keeps the code around. */
  onComplete: (params: { state: string; code: string }) => Promise<void>
  /** One line above the buttons explaining what the button does. */
  description: string
  /** Shown in the paste field, so the expected shape is obvious. */
  redirectPlaceholder: string
  disclaimer: string
  /** Extra buttons for the same row, e.g. manual credential renewal. */
  actions?: ReactNode
  disabled?: boolean
  signInLabel: string
}

export function LoopbackOAuthSignIn({
  onStart,
  onComplete,
  description,
  redirectPlaceholder,
  disclaimer,
  actions,
  disabled = false,
  signInLabel,
}: LoopbackOAuthSignInProps) {
  const { t } = useTranslation()
  const [authUrl, setAuthUrl] = useState('')
  const [state, setState] = useState('')
  const [code, setCode] = useState('')
  const [isStarting, setIsStarting] = useState(false)
  const [isCompleting, setIsCompleting] = useState(false)

  const handleStart = useCallback(async () => {
    setIsStarting(true)
    try {
      const started = await onStart()
      setAuthUrl(started.authUrl)
      setState(started.state)
      setCode('')
      window.open(started.authUrl, '_blank', 'noopener,noreferrer')
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to start sign-in')
      )
    } finally {
      setIsStarting(false)
    }
  }, [onStart, t])

  const handleComplete = useCallback(async () => {
    if (!state || !code.trim()) {
      return
    }
    setIsCompleting(true)
    try {
      await onComplete({ state, code: code.trim() })
      // Only clear on success; a failed exchange keeps what was pasted so it can
      // be corrected instead of copied again.
      setAuthUrl('')
      setState('')
      setCode('')
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Sign-in failed'))
    } finally {
      setIsCompleting(false)
    }
  }, [code, onComplete, state, t])

  return (
    <div className='border-border/60 flex flex-col gap-3 border-y py-4'>
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
        <div className='text-muted-foreground text-xs'>{description}</div>
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={handleStart}
            disabled={disabled || isStarting}
          >
            {isStarting ? (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' />
            ) : (
              <ExternalLink className='mr-2 h-4 w-4' />
            )}
            {signInLabel}
          </Button>
          {actions}
        </div>
      </div>

      {state && (
        <div className='flex flex-col gap-2'>
          <div className='text-muted-foreground text-xs'>
            {t(
              'After consenting, the browser lands on a localhost address that cannot load. Copy that whole address here — the authorization code is in it.'
            )}
          </div>
          {authUrl && (
            <a
              href={authUrl}
              target='_blank'
              rel='noopener noreferrer'
              className='text-primary text-xs underline underline-offset-2'
            >
              {t('Sign-in page did not open? Open it here')}
            </a>
          )}
          <div className='flex flex-col gap-2 sm:flex-row'>
            <Input
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder={redirectPlaceholder}
              className='font-mono'
              disabled={isCompleting}
            />
            <Button
              type='button'
              size='sm'
              onClick={handleComplete}
              disabled={isCompleting || !code.trim()}
            >
              {isCompleting && (
                <Loader2 className='mr-2 h-4 w-4 animate-spin' />
              )}
              {t('Finish sign-in')}
            </Button>
          </div>
        </div>
      )}

      <Alert className='border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-50'>
        <AlertDescription>{disclaimer}</AlertDescription>
      </Alert>
    </div>
  )
}
