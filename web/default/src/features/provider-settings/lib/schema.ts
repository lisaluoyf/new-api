import { z } from 'zod'

function createProviderRuleSchema(translate: (key: string) => string) {
  return z.object({
    enabled: z.boolean(),
    model: z
      .string()
      .trim()
      .min(1, translate('Select a model'))
      .max(200, translate('Invalid provider rule model')),
    mode: z.enum(['include', 'exclude'], {
      error: translate('Invalid provider rule mode'),
    }),
    channel_ids: z
      .array(
        z
          .number()
          .int(translate('Invalid channel ID'))
          .positive(translate('Invalid channel ID'))
      )
      .min(1, translate('Select at least one channel'))
      .max(512, translate('Too many selected channels')),
  })
}

export function createProviderSettingsSchema(
  translate: (key: string) => string = (key) => key
) {
  return z
    .object({
      rules: z
        .array(createProviderRuleSchema(translate))
        .max(128, translate('Too many provider rules')),
    })
    .superRefine((values, context) => {
      const seen = new Set<string>()
      values.rules.forEach((rule, index) => {
        if (seen.has(rule.model)) {
          context.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['rules', index, 'model'],
            message: translate('Model ID must be unique'),
          })
        }
        seen.add(rule.model)
      })
    })
}

export const providerRuleSchema = createProviderRuleSchema((key) => key)
export const providerSettingsSchema = createProviderSettingsSchema()
export type ProviderRule = z.infer<typeof providerRuleSchema>
export type ProviderSettingsValues = z.infer<typeof providerSettingsSchema>
