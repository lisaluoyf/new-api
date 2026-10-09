import { createInstance } from 'i18next'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { createProviderSettingsSchema } from './schema'

const localeRoot = new URL('../../../i18n/', import.meta.url)
const config = readFileSync(new URL('config.ts', localeRoot), 'utf8')
const supportedLanguages = [
  ...config.matchAll(/supportedLngs:\s*\[([^\]]+)\]/g),
].flatMap((match) =>
  [...match[1].matchAll(/'([^']+)'/g)].map((item) => item[1])
)
const sources = [
  new URL('../index.tsx', import.meta.url),
  new URL('../components/channel-picker.tsx', import.meta.url),
  new URL('schema.ts', import.meta.url),
].map((source) => readFileSync(source, 'utf8'))
const moduleKeys = new Set([
  ...sources.flatMap((source) =>
    [...source.matchAll(/\b(?:t|translate)\(\s*'([^']+)'/g)].map(
      (match) => match[1]
    )
  ),
  'Provider setting',
  'Invalid provider settings',
  'Channel is not available for this model',
  'Model is not available for provider settings',
  'image',
  'second',
])
const removedDescription =
  'These rules only filter eligible channels. Pricing, health checks, feedback, retries and fallbacks remain unchanged within the allowed channels. If none are available, the request fails. FreeModel is not affected.'

test('provider settings labels and errors exist in every supported language', () => {
  assert.equal(supportedLanguages.length, 15)
  for (const language of supportedLanguages) {
    const { translation } = JSON.parse(
      readFileSync(new URL(`locales/${language}.json`, localeRoot), 'utf8')
    ) as { translation: Record<string, string> }
    for (const key of moduleKeys) {
      assert.ok(translation[key]?.trim(), `${language}: missing ${key}`)
    }
    if (language !== 'en') {
      for (const key of ['Provider setting', 'Add rule']) {
        assert.notEqual(
          translation[key],
          key,
          `${language}: untranslated ${key}`
        )
      }
    }
    assert.equal(translation[removedDescription], undefined)
  }
  assert.ok(sources.every((source) => !source.includes(removedDescription)))
})

test('Japanese translates navigation, buttons, selections and validation', async () => {
  const japanese = JSON.parse(
    readFileSync(new URL('locales/ja.json', localeRoot), 'utf8')
  )
  const english = JSON.parse(
    readFileSync(new URL('locales/en.json', localeRoot), 'utf8')
  )
  const translator = createInstance()
  await translator.init({
    lng: 'ja',
    fallbackLng: 'en',
    resources: { ja: japanese, en: english },
  })
  assert.equal(translator.t('Provider setting'), 'プロバイダー設定')
  assert.equal(translator.t('Add rule'), 'ルールを追加')
  for (const key of moduleKeys) {
    assert.notEqual(translator.t(key), key, `Japanese fallback: ${key}`)
  }
  const schema = createProviderSettingsSchema((key) => translator.t(key))
  const invalid = schema.safeParse({
    rules: [{ enabled: true, model: '', mode: 'include', channel_ids: [] }],
  })
  assert.equal(invalid.success, false)
  if (!invalid.success) {
    assert.deepEqual(
      invalid.error.issues.map((issue) => issue.message),
      [
        translator.t('Select a model'),
        translator.t('Select at least one channel'),
      ]
    )
  }
  const rule = {
    enabled: true,
    model: 'gpt-image-2',
    mode: 'exclude',
    channel_ids: [38],
  }
  const duplicate = schema.safeParse({ rules: [rule, rule] })
  assert.equal(duplicate.success, false)
  if (!duplicate.success) {
    assert.equal(
      duplicate.error.issues[0].message,
      translator.t('Model ID must be unique')
    )
  }
  assert.equal(schema.safeParse({ rules: [rule] }).success, true)
})

test('channel discounts use the discount phrase instead of the off switch label', async () => {
  const translator = createInstance()
  const resources = Object.fromEntries(
    ['en', 'zh', 'ja'].map((language) => [
      language,
      JSON.parse(
        readFileSync(new URL(`locales/${language}.json`, localeRoot), 'utf8')
      ),
    ])
  )
  await translator.init({ lng: 'en', fallbackLng: 'en', resources })
  assert.equal(translator.t('{{discount}}% off', { discount: 20 }), '20% off')
  await translator.changeLanguage('zh')
  assert.equal(translator.t('{{discount}}% off', { discount: 20 }), '优惠 20%')
  await translator.changeLanguage('ja')
  assert.equal(translator.t('{{discount}}% off', { discount: 20 }), '20% 割引')
  assert.ok(!sources[1].includes("t('off')"))
  assert.ok(sources[1].includes("t('{{discount}}% off', { discount })"))
})
