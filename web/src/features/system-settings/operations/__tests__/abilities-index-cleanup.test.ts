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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  abilitiesIndexCleanupSchema,
  buildAbilitiesIndexCleanupDefaults,
} from '../../maintenance/abilities-index-cleanup-form'

const validDefaults = {
  'abilities_index_cleanup_setting.enabled': true,
  'abilities_index_cleanup_setting.interval_hours': 24,
  'abilities_index_cleanup_setting.auto_disabled_threshold_hours': 48,
  'abilities_index_cleanup_setting.batch_size': 100,
}

describe('ability index cleanup settings', () => {
  test('maps flat server options into the nested form model', () => {
    assert.deepEqual(buildAbilitiesIndexCleanupDefaults(validDefaults), {
      abilities_index_cleanup_setting: {
        enabled: true,
        interval_hours: 24,
        auto_disabled_threshold_hours: 48,
        batch_size: 100,
      },
    })
  })

  test('accepts operational bounds and rejects unsafe values', () => {
    assert.equal(
      abilitiesIndexCleanupSchema.safeParse(
        buildAbilitiesIndexCleanupDefaults(validDefaults)
      ).success,
      true
    )

    const invalid = buildAbilitiesIndexCleanupDefaults({
      ...validDefaults,
      'abilities_index_cleanup_setting.interval_hours': 0,
      'abilities_index_cleanup_setting.batch_size': 1001,
    })
    assert.equal(abilitiesIndexCleanupSchema.safeParse(invalid).success, false)
  })
})
