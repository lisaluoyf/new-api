import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { buildDiscordOAuthUrl } from './oauth'

describe('buildDiscordOAuthUrl', () => {
  test('uses space-delimited Discord scopes and the configured callback', () => {
    const result = new URL(
      buildDiscordOAuthUrl('client-id', 'oauth-state', 'https://apimaster.ai')
    )

    assert.equal(result.origin, 'https://discord.com')
    assert.equal(result.pathname, '/oauth2/authorize')
    assert.equal(result.searchParams.get('client_id'), 'client-id')
    assert.equal(
      result.searchParams.get('redirect_uri'),
      'https://apimaster.ai/oauth/discord'
    )
    assert.equal(result.searchParams.get('scope'), 'identify openid')
    assert.equal(result.searchParams.get('state'), 'oauth-state')
  })
})
