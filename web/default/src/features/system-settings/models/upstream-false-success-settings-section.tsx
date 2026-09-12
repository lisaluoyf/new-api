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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const OPTION_KEY = 'upstream_false_success_setting.enabled'

interface Props {
  defaultValue: boolean
}

export function UpstreamFalseSuccessSettingsSection(props: Props) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [enabled, setEnabled] = useState(props.defaultValue)

  useEffect(() => {
    setEnabled(props.defaultValue)
  }, [props.defaultValue])

  const handleChange = (next: boolean) => {
    setEnabled(next)
    updateOption.mutate(
      { key: OPTION_KEY, value: String(next) },
      {
        onError: () => {
          setEnabled(props.defaultValue)
        },
      }
    )
  }

  return (
    <SettingsSection
      title={t('HTTP 200 False Success Fallback')}
      description={t(
        'Detect upstream HTTP 200 responses that carry an error or no usable output, then fall back to another channel. Turning this off stops the detection, the fallback, the diagnostic logs and the notifications; stream keep-alive stays suppressed until the first upstream frame so the response is not committed too early.'
      )}
    >
      <div className="rounded-lg border">
        <div className="flex flex-col gap-3 px-4 py-3 lg:flex-row lg:items-start lg:justify-between">
          <div className="space-y-1">
            <p className="text-sm font-medium">
              {t('Enable false success fallback')}
            </p>
            <p className="text-muted-foreground text-sm">
              {t(
                'When disabled, HTTP 200 responses are never judged as false successes and never trigger a channel fallback. Failures that upstream already reports with an error still return 502 as before.'
              )}
            </p>
          </div>
          <Switch
            checked={enabled}
            onCheckedChange={handleChange}
            disabled={updateOption.isPending}
            aria-label={t('Enable false success fallback')}
          />
        </div>
      </div>
    </SettingsSection>
  )
}
