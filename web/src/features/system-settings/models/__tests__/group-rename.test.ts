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
import { describe, expect, it } from 'vitest'

import {
  effectiveGroupRenames,
  renameGroupsInSettings,
  trackGroupRename,
} from '../group-rename'

const saved = '{"default":1,"vip":0.8}'

function typeName(steps: string[]) {
  let renames = {}
  for (let i = 1; i < steps.length; i++) {
    renames = trackGroupRename(renames, steps[i - 1], steps[i])
  }
  return renames
}

describe('group renames', () => {
  it('maps the saved name to the final name when a name is typed over several keystrokes', () => {
    const tracked = typeName(['default', '', 'I', 'IT', 'IT分组'])

    expect(
      effectiveGroupRenames(tracked, saved, '{"IT分组":1,"vip":0.8}')
    ).toEqual({ default: 'IT分组' })
  })

  it('carries out no rename when a name is edited and then restored', () => {
    const tracked = typeName(['vip', 'vi', 'vip'])

    expect(effectiveGroupRenames(tracked, saved, saved)).toEqual({})
  })

  it('treats a renamed group that was then deleted as a deletion, not a rename', () => {
    const tracked = typeName(['vip', 'gold'])

    expect(effectiveGroupRenames(tracked, saved, '{"default":1}')).toEqual({})
  })

  it('ignores name edits on a group that was never saved', () => {
    const tracked = typeName(['group_1', 'image'])

    expect(
      effectiveGroupRenames(tracked, saved, '{"default":1,"vip":0.8,"image":1}')
    ).toEqual({})
  })

  it('renames groups in the auto order, override rules and prefixed visibility rules', () => {
    const result = renameGroupsInSettings(
      {
        AutoGroups: '["default","vip"]',
        GroupGroupRatio: '{"default":{"vip":0.5},"vip":{"default":2}}',
        GroupSpecialUsableGroup:
          '{"default":{"+:vip":"Premium","-:default":"","svip":"Top"}}',
      },
      { default: 'IT分组' }
    )

    expect(JSON.parse(result.AutoGroups)).toEqual(['IT分组', 'vip'])
    expect(JSON.parse(result.GroupGroupRatio)).toEqual({
      IT分组: { vip: 0.5 },
      vip: { IT分组: 2 },
    })
    expect(JSON.parse(result.GroupSpecialUsableGroup)).toEqual({
      IT分组: { '+:vip': 'Premium', '-:IT分组': '', svip: 'Top' },
    })
  })

  it('leaves a field that is not valid JSON for its own validation to report', () => {
    const result = renameGroupsInSettings(
      {
        AutoGroups: '["default"',
        GroupGroupRatio: '{}',
        GroupSpecialUsableGroup: '{}',
      },
      { default: 'IT分组' }
    )

    expect(result.AutoGroups).toBe('["default"')
  })
})
