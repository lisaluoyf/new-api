import assert from 'node:assert/strict'
import { test } from 'node:test'
import { downloadMediaFile } from './download-media'

test('a cached image removed during download does not open a dead link', async () => {
  const previousFetch = globalThis.fetch
  try {
    globalThis.fetch = async () => new Response('Not Found', { status: 404 })
    await assert.rejects(
      downloadMediaFile('/imgs/expired.png', 'image.png', {
        allowLinkFallback: false,
      }),
      /download failed: 404/
    )
  } finally {
    globalThis.fetch = previousFetch
  }
})
