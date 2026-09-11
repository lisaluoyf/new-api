import { createElement } from 'react'
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { ModelMappingEditor } from './model-mapping-editor'

function renderEditor(value: string) {
  return renderToStaticMarkup(
    createElement(ModelMappingEditor, {
      value,
      onChange: () =>
        assert.fail('Loading saved mappings must not change them'),
    })
  )
}

describe('ModelMappingEditor initial values', () => {
  test('displays saved mappings when reopening with already loaded channel data', () => {
    const html = renderEditor(
      JSON.stringify({ 'public-model': 'routed-model', alias: 'target' })
    )
    for (const value of ['public-model', 'routed-model', 'alias', 'target']) {
      assert.ok(html.includes(`value="${value}"`), `${value} must be visible`)
    }
    assert.ok(!html.includes('No model mappings configured.'))
  })

  test('shows an empty editor for a channel without mappings', () => {
    assert.ok(renderEditor('').includes('No model mappings configured.'))
  })

  test('does not crash or overwrite malformed saved JSON', () => {
    assert.ok(
      renderEditor('{invalid').includes('No model mappings configured.')
    )
  })
})
