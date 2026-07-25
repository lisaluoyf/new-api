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
import * as React from 'react'
import { Input } from '@/components/ui/input'

type NativeInputProps = Omit<
  React.ComponentProps<typeof Input>,
  'value' | 'onChange' | 'type'
>

export type NumericInputProps = NativeInputProps & {
  /** Numeric form value; `undefined` renders as empty. */
  value: number | undefined
  /** Called with a parsed number, or `undefined` when the field is empty. */
  onValueChange: (value: number | undefined) => void
}

// 中间态友好的数字输入：内部用字符串草稿保留 "2."、"" 等 type=number 无法表达的输入态，
// 只在解析成功时把 number|undefined 回写表单，避免受控 number input 每次按键被规范化后卡住/删不掉。
function NumericInput({ value, onValueChange, ...props }: NumericInputProps) {
  const [draft, setDraft] = React.useState<string>(
    value == null ? '' : String(value),
  )
  const [focused, setFocused] = React.useState(false)

  // 外部值变化（切换渠道、预设回填、reset）时，未聚焦则同步草稿；聚焦中不打断用户输入。
  React.useEffect(() => {
    if (focused) return
    const next = value == null ? '' : String(value)
    if (Number(draft) !== value || (draft === '' ) !== (value == null)) {
      setDraft(next)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, focused])

  return (
    <Input
      {...props}
      type='number'
      value={draft}
      onFocus={(e) => {
        setFocused(true)
        props.onFocus?.(e)
      }}
      onBlur={(e) => {
        setFocused(false)
        // 失焦时把草稿归一成规范数字文本（去掉尾随小数点/前导零）
        const parsed = e.target.value === '' ? undefined : Number(e.target.value)
        setDraft(parsed == null || Number.isNaN(parsed) ? '' : String(parsed))
        props.onBlur?.(e)
      }}
      onChange={(e) => {
        const raw = e.target.value
        setDraft(raw)
        if (raw === '') {
          onValueChange(undefined)
          return
        }
        const parsed = Number(raw)
        if (!Number.isNaN(parsed)) onValueChange(parsed)
      }}
    />
  )
}

export { NumericInput }
