import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  selectEvmProvider,
  type EIP6963ProviderDetail,
  type EthereumProvider,
} from './evm-provider'

function provider(flags: Partial<EthereumProvider> = {}): EthereumProvider {
  return {
    request: async () => null,
    ...flags,
  }
}

function announced(
  walletProvider: EthereumProvider,
  uuid: string,
  rdns: string
): EIP6963ProviderDetail {
  return {
    info: { uuid, name: uuid, rdns },
    provider: walletProvider,
  }
}

describe('selectEvmProvider', () => {
  test('keeps the provider selected by the legacy injection path', () => {
    const phantom = provider({ isPhantom: true })
    const trust = provider({ isTrust: true })

    assert.equal(
      selectEvmProvider(
        [
          announced(trust, 'trust', 'com.trustwallet.app'),
          announced(phantom, 'phantom', 'app.phantom'),
        ],
        phantom
      ),
      phantom
    )
  })

  test('uses an EIP-6963 provider when legacy injection is unavailable', () => {
    const trust = provider({ isTrust: true })

    assert.equal(
      selectEvmProvider(
        [announced(trust, 'trust', 'com.trustwallet.app')],
        null
      ),
      trust
    )
  })

  test('recognizes Trust Wallet among multiple EIP-6963 providers', () => {
    const other = provider()
    const trust = provider({ isTrustWallet: true })

    assert.equal(
      selectEvmProvider(
        [
          announced(other, 'other', 'io.example.wallet'),
          announced(trust, 'trust', 'com.trustwallet.app'),
        ],
        null
      ),
      trust
    )
  })

  test('continues to support a legacy-only provider', () => {
    const legacy = provider({ isMetaMask: true })
    assert.equal(selectEvmProvider([], legacy), legacy)
  })
})
