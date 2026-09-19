import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { shouldRediscoverCryptoWallets } from './crypto-wallet-provider'

describe('shouldRediscoverCryptoWallets', () => {
  test('keeps detected wallets when switching within the same ecosystem', () => {
    assert.equal(shouldRediscoverCryptoWallets('evm', 'evm'), false)
    assert.equal(shouldRediscoverCryptoWallets('tron', 'tron'), false)
    assert.equal(shouldRediscoverCryptoWallets('solana', 'solana'), false)
  })

  test('rediscovers wallets when switching ecosystems', () => {
    assert.equal(shouldRediscoverCryptoWallets('evm', 'tron'), true)
    assert.equal(shouldRediscoverCryptoWallets('tron', 'solana'), true)
    assert.equal(shouldRediscoverCryptoWallets('solana', 'evm'), true)
  })
})
