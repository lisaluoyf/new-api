import assert from 'node:assert/strict'
import { test } from 'node:test'
import en from '../../i18n/locales/en.json'
import zhTW from '../../i18n/locales/zh-TW.json'
import zh from '../../i18n/locales/zh.json'

test('resolution controls use the locale translation namespace', () => {
  for (const locale of [en, zh, zhTW]) {
    assert.ok(locale.translation['Seedance resolution selection'])
    assert.ok(locale.translation['All resolutions'])
    assert.ok(locale.translation['No resolutions'])
    assert.ok(
      locale.translation[
        'Unchecked resolutions fall back to another channel. Input means reference video.'
      ]
    )
    assert.equal(Object.hasOwn(locale, 'All resolutions'), false)
  }
  assert.equal(zh.translation['All resolutions'], '全部分辨率')
  assert.equal(zhTW.translation['All resolutions'], '所有解析度')
})
