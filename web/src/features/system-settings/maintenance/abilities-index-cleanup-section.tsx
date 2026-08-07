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
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
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
import { Progress } from '@/components/ui/progress'
import { Switch } from '@/components/ui/switch'

import {
  getAbilitiesIndexCleanupTask,
  getCurrentAbilitiesIndexCleanupTask,
  startAbilitiesIndexCleanupTask,
} from '../api'
import {
  SettingsControlGroup,
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import type { AbilitiesIndexCleanupTask } from '../types'
import { safeNumberFieldProps } from '../utils/numeric-field'
import {
  abilitiesIndexCleanupSchema,
  buildAbilitiesIndexCleanupDefaults,
  flattenAbilitiesIndexCleanupValues,
  type AbilitiesIndexCleanupDefaults,
  type AbilitiesIndexCleanupFormInput,
  type AbilitiesIndexCleanupFormValues,
} from './abilities-index-cleanup-form'

function isActive(task: AbilitiesIndexCleanupTask | null) {
  return task?.status === 'pending' || task?.status === 'running'
}

export function AbilitiesIndexCleanupSection({
  defaultValues,
}: {
  defaultValues: AbilitiesIndexCleanupDefaults
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const baselineRef = useRef(defaultValues)
  const baselineSerializedRef = useRef(JSON.stringify(defaultValues))
  const [persistedEnabled, setPersistedEnabled] = useState(
    defaultValues['abilities_index_cleanup_setting.enabled']
  )
  const [task, setTask] = useState<AbilitiesIndexCleanupTask | null>(null)
  const [isStarting, setIsStarting] = useState(false)
  const form = useForm<
    AbilitiesIndexCleanupFormInput,
    unknown,
    AbilitiesIndexCleanupFormValues
  >({
    resolver: zodResolver(abilitiesIndexCleanupSchema),
    defaultValues: buildAbilitiesIndexCleanupDefaults(defaultValues),
  })

  useEffect(() => {
    const serialized = JSON.stringify(defaultValues)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = defaultValues
    baselineSerializedRef.current = serialized
    setPersistedEnabled(
      defaultValues['abilities_index_cleanup_setting.enabled']
    )
    form.reset(buildAbilitiesIndexCleanupDefaults(defaultValues))
  }, [defaultValues, form])

  useEffect(() => {
    let cancelled = false
    getCurrentAbilitiesIndexCleanupTask()
      .then((response) => {
        if (!cancelled && response.success) setTask(response.data ?? null)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  const active = isActive(task)
  const taskId = task?.task_id
  useEffect(() => {
    if (!active || !taskId) return
    let cancelled = false
    const interval = window.setInterval(async () => {
      try {
        const response = await getAbilitiesIndexCleanupTask(taskId)
        if (cancelled || !response.success || !response.data) return
        setTask(response.data)
        if (!isActive(response.data)) {
          if (response.data.status === 'succeeded') {
            toast.success(
              t('Ability index cleanup completed: {{count}} channels pruned.', {
                count: response.data.result?.pruned ?? 0,
              })
            )
          } else {
            toast.error(
              response.data.error || t('Ability index cleanup failed')
            )
          }
        }
      } catch {
        // Keep polling through transient failures.
      }
    }, 1000)
    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [active, taskId, t])

  const onSubmit = async (values: AbilitiesIndexCleanupFormValues) => {
    const normalized = flattenAbilitiesIndexCleanupValues(values)
    const keys: Array<keyof AbilitiesIndexCleanupDefaults> = [
      'abilities_index_cleanup_setting.interval_hours',
      'abilities_index_cleanup_setting.auto_disabled_threshold_hours',
      'abilities_index_cleanup_setting.batch_size',
      'abilities_index_cleanup_setting.enabled',
    ]
    const changedKeys = keys.filter(
      (key) => normalized[key] !== baselineRef.current[key]
    )
    if (changedKeys.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    for (const key of changedKeys) {
      const response = await updateOption.mutateAsync({
        key,
        value: normalized[key],
      })
      if (!response.success) return
    }
    baselineRef.current = normalized
    baselineSerializedRef.current = JSON.stringify(normalized)
    setPersistedEnabled(normalized['abilities_index_cleanup_setting.enabled'])
    form.reset(buildAbilitiesIndexCleanupDefaults(normalized))
  }

  const runNow = async () => {
    setIsStarting(true)
    try {
      const response = await startAbilitiesIndexCleanupTask()
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Ability index cleanup failed'))
      }
      setTask(response.data)
      toast.success(t('Ability index cleanup task started.'))
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Ability index cleanup failed')
      )
    } finally {
      setIsStarting(false)
    }
  }

  const settingEnabled = form.watch('abilities_index_cleanup_setting.enabled')
  const progress = Math.min(100, Math.max(0, task?.state?.progress ?? 0))

  return (
    <SettingsSection title={t('Ability Index Cleanup')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='abilities_index_cleanup_setting.enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable ability index cleanup')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Periodically remove ability rows for manually disabled channels and long-disabled automatic channels.'
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
          <div className='grid grid-cols-1 gap-4 md:grid-cols-3'>
            <NumberField
              form={form}
              name='abilities_index_cleanup_setting.interval_hours'
              label={t('Cleanup interval (hours)')}
              min={1}
              max={720}
              disabled={!settingEnabled}
            />
            <NumberField
              form={form}
              name='abilities_index_cleanup_setting.auto_disabled_threshold_hours'
              label={t('Auto-disabled retention (hours)')}
              min={1}
              max={8760}
              disabled={!settingEnabled}
            />
            <NumberField
              form={form}
              name='abilities_index_cleanup_setting.batch_size'
              label={t('Cleanup batch size')}
              min={1}
              max={1000}
              disabled={!settingEnabled}
            />
          </div>
          <SettingsControlGroup className='space-y-3'>
            <div>
              <h4 className='text-sm font-medium'>{t('Run cleanup now')}</h4>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Eligibility is derived from channel status and automatic-disable time; no channel exclusion field is stored.'
                )}
              </p>
            </div>
            {task && (
              <div className='space-y-2'>
                <Progress value={progress} />
                <p className='text-muted-foreground text-xs'>
                  {active
                    ? t(
                        '{{processed}} of {{total}} disabled channel candidates processed',
                        {
                          processed: task.state?.processed ?? 0,
                          total: task.state?.total ?? 0,
                        }
                      )
                    : t('Last cleanup status: {{status}}', {
                        status: t(task.status),
                      })}
                </p>
              </div>
            )}
            <Button
              type='button'
              variant='outline'
              onClick={runNow}
              disabled={!persistedEnabled || active || isStarting}
            >
              {active || isStarting
                ? t('Cleanup running...')
                : t('Run cleanup now')}
            </Button>
            {!persistedEnabled && (
              <p className='text-muted-foreground text-xs'>
                {t('Save and enable cleanup before running it manually.')}
              </p>
            )}
          </SettingsControlGroup>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}

type NumberFieldName =
  | 'abilities_index_cleanup_setting.interval_hours'
  | 'abilities_index_cleanup_setting.auto_disabled_threshold_hours'
  | 'abilities_index_cleanup_setting.batch_size'

function NumberField({
  form,
  name,
  label,
  min,
  max,
  disabled,
}: {
  form: ReturnType<
    typeof useForm<
      AbilitiesIndexCleanupFormInput,
      unknown,
      AbilitiesIndexCleanupFormValues
    >
  >
  name: NumberFieldName
  label: string
  min: number
  max: number
  disabled: boolean
}) {
  return (
    <FormField
      control={form.control}
      name={name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <Input
              type='number'
              min={min}
              max={max}
              step={1}
              {...safeNumberFieldProps(field)}
              disabled={disabled}
            />
          </FormControl>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}
