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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { CursorAccount } from '../../api'
import { CHANNEL_TYPE_CURSOR } from '../../constants'
import { channelSchema } from '../../types'
import { ChannelRowActionsLayoutContext } from '../channel-row-actions-context'
import { BalanceCell } from '../channels-columns'
import { ChannelsDialogs } from '../channels-dialogs'
import { ChannelsProvider } from '../channels-provider'
import { CursorAccountPanel } from '../cursor-account-panel'

const originalAuth = useAuthStore.getState().auth
let client: QueryClient

const signedIn: CursorAccount = {
  authenticated: true,
  email: 'ops@example.com',
  subscription_tier: 'Pro',
}
const signedOut: CursorAccount = { authenticated: false }
const loginLink = 'https://cursor.com/loginDeepControl?uuid=test'

function mockAccount(...accounts: CursorAccount[]) {
  const queue = [...accounts]
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel/42/cursor/account') {
      const next = queue.length > 1 ? queue.shift() : queue[0]
      return { data: { success: true, data: next } }
    }
    return { data: { success: true, data: [] } }
  })
}

function renderPanel(channelId?: number) {
  return render(
    <QueryClientProvider client={client}>
      <CursorAccountPanel channelId={channelId} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  useAuthStore.setState({
    auth: {
      ...originalAuth,
      user: { id: 1, username: 'root', role: ROLE.SUPER_ADMIN },
    },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.setState({ auth: originalAuth })
})

it('opens the Cursor account from the balance cell instead of a balance query', async () => {
  const get = mockAccount(signedIn)
  const channel = channelSchema.parse({
    id: 42,
    type: CHANNEL_TYPE_CURSOR,
    key: '',
    name: 'Cursor Pro',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <ChannelsProvider>
        <ChannelRowActionsLayoutContext.Provider value='table'>
          <BalanceCell channel={channel} />
        </ChannelRowActionsLayoutContext.Provider>
        <ChannelsDialogs />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  await user.click(screen.getByRole('button', { name: 'Cursor account' }))
  const dialog = await screen.findByRole('dialog', { name: 'Cursor account' })
  expect(await within(dialog).findByText('ops@example.com')).toBeInTheDocument()
  expect(within(dialog).getByText('Plan: Pro')).toBeInTheDocument()
  expect(get.mock.calls.some(([url]) => url.includes('update_balance'))).toBe(
    false
  )
})

it('starts a sign-in, shows the link and picks up the finished login', async () => {
  mockAccount(signedOut, signedIn)
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: { status: 'pending', url: loginLink, started_at: 'now' },
    },
  })
  const open = vi.spyOn(window, 'open').mockImplementation(() => null)
  const user = userEvent.setup()
  renderPanel(42)

  expect(await screen.findByText('Not signed in')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Sign in to Cursor' }))

  expect(post).toHaveBeenCalledWith(
    '/api/channel/42/cursor/login',
    { restart: false },
    expect.anything()
  )
  expect(open).toHaveBeenCalledWith(loginLink, '_blank', 'noopener,noreferrer')
  expect(
    screen.getByRole('link', { name: 'Open the Cursor sign-in page' })
  ).toHaveAttribute('href', loginLink)

  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('ops@example.com')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Switch account' })).toBeEnabled()
})

it('switches account with a fresh link and signs out after confirming', async () => {
  mockAccount(signedIn)
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/channel/42/cursor/logout') {
      return { data: { success: true, data: signedOut } }
    }
    return {
      data: { success: true, data: { status: 'pending', url: loginLink } },
    }
  })
  vi.spyOn(window, 'open').mockImplementation(() => null)
  const user = userEvent.setup()
  renderPanel(42)

  await user.click(await screen.findByRole('button', { name: 'Switch account' }))
  expect(post).toHaveBeenCalledWith(
    '/api/channel/42/cursor/login',
    { restart: true },
    expect.anything()
  )

  await user.click(screen.getByRole('button', { name: 'Sign out' }))
  const confirm = await screen.findByRole('alertdialog')
  await user.click(within(confirm).getByRole('button', { name: 'Sign out' }))
  expect(post).toHaveBeenCalledWith(
    '/api/channel/42/cursor/logout',
    {},
    expect.anything()
  )
  expect(await screen.findByText('Not signed in')).toBeInTheDocument()
})

it('asks for the channel to be saved before there is a bridge to sign in', () => {
  const get = vi.spyOn(api, 'get')
  renderPanel(undefined)
  expect(
    screen.getByText('Save the channel first, then sign in to Cursor here.')
  ).toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

it('reports an unreachable bridge instead of offering a sign-in', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: false, message: 'Cannot reach the Cursor bridge' },
  })
  renderPanel(42)
  expect(
    await screen.findByText('Cannot reach the Cursor bridge')
  ).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Sign in to Cursor' })).toBeDisabled()
})
