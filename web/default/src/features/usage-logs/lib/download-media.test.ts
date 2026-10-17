import assert from 'node:assert/strict'
import { test } from 'node:test'
import { downloadMediaFile } from './download-media'

test('a cached image removed during download does not open a dead link', async () => {
  const previousFetch = globalThis.fetch
  let credentials: RequestCredentials | undefined
  try {
    globalThis.fetch = async (_url, options) => {
      credentials = options?.credentials
      return new Response('Not Found', { status: 404 })
    }
    await assert.rejects(
      downloadMediaFile('/imgs/expired.png', 'image.png', {
        allowLinkFallback: false,
        credentials: 'same-origin',
      }),
      /download failed: 404/
    )
    assert.equal(credentials, 'same-origin')
  } finally {
    globalThis.fetch = previousFetch
  }
})
