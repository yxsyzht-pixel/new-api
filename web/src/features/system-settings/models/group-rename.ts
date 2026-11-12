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
import { safeJsonParse } from '../utils/json-parser'

/** Saved group name → the name it is being renamed to. */
export type GroupRenames = Record<string, string>

type GroupReferenceSettings = {
  AutoGroups: string
  GroupGroupRatio: string
  GroupSpecialUsableGroup: string
}

const specialGroupPrefixes = ['+:', '-:']

function parseObject<T>(value: string): T | null {
  const parsed = safeJsonParse<unknown>(value, { fallback: null, silent: true })
  if (parsed === null || typeof parsed !== 'object') return null
  return parsed as T
}

/**
 * Follows one edit of a group's name. Typing a new name changes it one
 * keystroke at a time, so the edit continues whichever rename already ends
 * at the previous name; otherwise the previous name is where it starts.
 */
export function trackGroupRename(
  renames: GroupRenames,
  from: string,
  to: string
): GroupRenames {
  if (from === to) return renames
  const origin =
    Object.keys(renames).find((saved) => renames[saved] === from) ?? from
  return { ...renames, [origin]: to }
}

/**
 * The renames a save carries out: a saved group whose name is gone from the
 * new pricing table and whose new name is in it. A name that was edited and
 * then restored, or a group that was deleted, is not a rename.
 */
export function effectiveGroupRenames(
  tracked: GroupRenames,
  savedGroupRatio: string,
  nextGroupRatio: string
): GroupRenames {
  const saved = new Set(
    Object.keys(parseObject<Record<string, number>>(savedGroupRatio) ?? {})
  )
  const next = new Set(
    Object.keys(parseObject<Record<string, number>>(nextGroupRatio) ?? {})
  )
  const renames: GroupRenames = {}
  for (const [from, to] of Object.entries(tracked)) {
    if (saved.has(from) && !next.has(from) && to && next.has(to)) {
      renames[from] = to
    }
  }
  return renames
}

function renameSpecialGroupKey(key: string, renames: GroupRenames): string {
  const prefix = specialGroupPrefixes.find((item) => key.startsWith(item))
  if (!prefix) return renames[key] ?? key
  const name = key.slice(prefix.length)
  return prefix + (renames[name] ?? name)
}

function renameKeys<T>(
  map: Record<string, T>,
  renameKey: (key: string) => string
): Record<string, T> {
  return Object.fromEntries(
    Object.entries(map).map(([key, value]) => [renameKey(key), value])
  )
}

/**
 * Applies renames to the settings that name groups besides the pricing table:
 * the auto group order, the per-user-group ratio overrides, and the special
 * usable-group rules (whose inner keys may carry a +: or -: prefix). A field
 * that is not valid JSON is returned unchanged for its own validation to flag.
 */
export function renameGroupsInSettings<T extends GroupReferenceSettings>(
  values: T,
  renames: GroupRenames
): T {
  if (Object.keys(renames).length === 0) return values
  const rename = (name: string) => renames[name] ?? name
  const result = { ...values }

  const autoGroups = safeJsonParse<unknown>(values.AutoGroups, {
    fallback: null,
    silent: true,
  })
  if (Array.isArray(autoGroups)) {
    const renamed = autoGroups.map((name) =>
      typeof name === 'string' ? rename(name) : name
    )
    result.AutoGroups = JSON.stringify([...new Set(renamed)], null, 2)
  }

  const overrides = parseObject<Record<string, Record<string, number>>>(
    values.GroupGroupRatio
  )
  if (overrides) {
    const renamed = renameKeys(overrides, rename)
    for (const userGroup of Object.keys(renamed)) {
      renamed[userGroup] = renameKeys(renamed[userGroup] ?? {}, rename)
    }
    result.GroupGroupRatio = JSON.stringify(renamed, null, 2)
  }

  const special = parseObject<Record<string, Record<string, string>>>(
    values.GroupSpecialUsableGroup
  )
  if (special) {
    const renamed = renameKeys(special, rename)
    for (const userGroup of Object.keys(renamed)) {
      renamed[userGroup] = renameKeys(renamed[userGroup] ?? {}, (key) =>
        renameSpecialGroupKey(key, renames)
      )
    }
    result.GroupSpecialUsableGroup = JSON.stringify(renamed, null, 2)
  }

  return result
}
