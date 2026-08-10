/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { UserAnalyticsMetric } from '@/features/dashboard/types'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { UserMetricTabs } = await import('../user-metric-tabs')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Type: 'Type',
        Amount: 'Amount',
        Tokens: 'Tokens',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function Harness() {
  const [metric, setMetric] = useState<UserAnalyticsMetric>('quota')

  return (
    <I18nextProvider i18n={i18n}>
      <UserMetricTabs value={metric} onValueChange={setMetric} />
      <output data-testid='selected-metric'>{metric}</output>
    </I18nextProvider>
  )
}

describe('user analytics metric tabs', () => {
  after(() => {
    domWindow.close()
  })

  test('switches the selected user analytics metric from amount to Tokens', async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => root.render(<Harness />))

    const tabs = container.querySelector('[role="tablist"]')
    assert.ok(tabs)
    assert.equal(tabs.getAttribute('aria-label'), 'Type')

    const tokenTab = [
      ...container.querySelectorAll<HTMLButtonElement>('[role="tab"]'),
    ].find((tab) => tab.textContent === 'Tokens')
    assert.ok(tokenTab)
    await act(async () => tokenTab.click())

    assert.equal(
      container.querySelector('[data-testid="selected-metric"]')?.textContent,
      'tokens'
    )
    assert.equal(tokenTab.getAttribute('aria-selected'), 'true')

    await act(async () => root.unmount())
    container.remove()
  })
})
