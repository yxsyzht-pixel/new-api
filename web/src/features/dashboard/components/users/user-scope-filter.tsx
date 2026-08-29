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

import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

export const ALL_USERS = '__all__'

interface UserScopeFilterProps {
  /** Accounts present in the loaded window, already sorted by spend. */
  users: string[]
  value?: string
  onValueChange: (username?: string) => void
}

/**
 * Narrows the key charts to a single account.
 *
 * The ranking is global by default, and one account running long autonomous
 * turns can take most of the top ten on its own — which is a true picture of
 * spend and a useless one for "how are this team's keys being used". Choosing
 * an account re-ranks within it.
 *
 * The list comes from the data already on screen rather than from the user
 * directory: an account with no traffic in the window has no keys to show, and
 * offering it would only produce an empty chart.
 */
export function UserScopeFilter(props: UserScopeFilterProps) {
  const { t } = useTranslation()

  return (
    <Select
      value={props.value || ALL_USERS}
      onValueChange={(value) =>
        props.onValueChange(
          !value || value === ALL_USERS ? undefined : String(value)
        )
      }
    >
      <SelectTrigger size='sm' className='h-8 w-40 shrink-0 text-xs'>
        <SelectValue placeholder={t('All Users')} />
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectGroup>
          <SelectItem value={ALL_USERS}>{t('All Users')}</SelectItem>
          {props.users.map((username) => (
            <SelectItem key={username} value={username}>
              {username}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}
