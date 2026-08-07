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
import * as z from 'zod'

export const abilitiesIndexCleanupSchema = z.object({
  abilities_index_cleanup_setting: z.object({
    enabled: z.boolean(),
    interval_hours: z.coerce.number().int().min(1).max(720),
    auto_disabled_threshold_hours: z.coerce.number().int().min(1).max(8760),
    batch_size: z.coerce.number().int().min(1).max(1000),
  }),
})

export type AbilitiesIndexCleanupFormInput = z.input<
  typeof abilitiesIndexCleanupSchema
>
export type AbilitiesIndexCleanupFormValues = z.output<
  typeof abilitiesIndexCleanupSchema
>

export type AbilitiesIndexCleanupDefaults = {
  'abilities_index_cleanup_setting.enabled': boolean
  'abilities_index_cleanup_setting.interval_hours': number
  'abilities_index_cleanup_setting.auto_disabled_threshold_hours': number
  'abilities_index_cleanup_setting.batch_size': number
}

export function buildAbilitiesIndexCleanupDefaults(
  defaults: AbilitiesIndexCleanupDefaults
): AbilitiesIndexCleanupFormInput {
  return {
    abilities_index_cleanup_setting: {
      enabled: defaults['abilities_index_cleanup_setting.enabled'],
      interval_hours:
        defaults['abilities_index_cleanup_setting.interval_hours'],
      auto_disabled_threshold_hours:
        defaults[
          'abilities_index_cleanup_setting.auto_disabled_threshold_hours'
        ],
      batch_size: defaults['abilities_index_cleanup_setting.batch_size'],
    },
  }
}

export function flattenAbilitiesIndexCleanupValues(
  values: AbilitiesIndexCleanupFormValues
): AbilitiesIndexCleanupDefaults {
  return {
    'abilities_index_cleanup_setting.enabled':
      values.abilities_index_cleanup_setting.enabled,
    'abilities_index_cleanup_setting.interval_hours':
      values.abilities_index_cleanup_setting.interval_hours,
    'abilities_index_cleanup_setting.auto_disabled_threshold_hours':
      values.abilities_index_cleanup_setting.auto_disabled_threshold_hours,
    'abilities_index_cleanup_setting.batch_size':
      values.abilities_index_cleanup_setting.batch_size,
  }
}
