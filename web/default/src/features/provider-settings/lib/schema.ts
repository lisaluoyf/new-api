import { z } from 'zod'

export const providerRuleSchema = z.object({
  enabled: z.boolean(),
  model: z.string().trim().min(1, 'Select a model').max(200),
  mode: z.enum(['include', 'exclude']),
  channel_ids: z
    .array(z.number().int().positive())
    .min(1, 'Select at least one channel')
    .max(512),
})

export const providerSettingsSchema = z
  .object({
    rules: z.array(providerRuleSchema).max(128),
  })
  .superRefine((values, context) => {
    const seen = new Set<string>()
    values.rules.forEach((rule, index) => {
      if (seen.has(rule.model)) {
        context.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['rules', index, 'model'],
          message: 'Model ID must be unique',
        })
      }
      seen.add(rule.model)
    })
  })

export type ProviderRule = z.infer<typeof providerRuleSchema>
export type ProviderSettingsValues = z.infer<typeof providerSettingsSchema>
