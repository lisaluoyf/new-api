/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

export interface EthereumProvider {
  request: (args: { method: string; params?: unknown[] }) => Promise<unknown>
  isMetaMask?: boolean
  isBinance?: boolean
  isPhantom?: boolean
  isTrust?: boolean
  isTrustWallet?: boolean
  providers?: EthereumProvider[]
}

interface EIP6963ProviderInfo {
  uuid: string
  name: string
  rdns: string
}

export interface EIP6963ProviderDetail {
  info: EIP6963ProviderInfo
  provider: EthereumProvider
}

export interface EvmWalletOption {
  id: string
  name: string
  provider: EthereumProvider
}

declare global {
  interface Window {
    ethereum?: EthereumProvider
    trustwallet?: EthereumProvider
    phantom?: {
      ethereum?: EthereumProvider
    }
  }
}

const announcedProviders = new Map<string, EIP6963ProviderDetail>()
const providerAnnouncementListeners = new Set<() => void>()
let discoveryInitialized = false

function isProviderDetail(value: unknown): value is EIP6963ProviderDetail {
  const detail = value as Partial<EIP6963ProviderDetail> | undefined
  return (
    typeof detail?.info?.uuid === 'string' &&
    typeof detail.info.name === 'string' &&
    typeof detail.info.rdns === 'string' &&
    typeof detail.provider?.request === 'function'
  )
}

function initializeEIP6963Discovery() {
  if (typeof window === 'undefined') return

  if (!discoveryInitialized) {
    window.addEventListener('eip6963:announceProvider', (event) => {
      const detail = (event as CustomEvent<unknown>).detail
      if (isProviderDetail(detail)) {
        announcedProviders.set(detail.info.uuid, detail)
        providerAnnouncementListeners.forEach((listener) => listener())
      }
    })
    discoveryInitialized = true
  }

  window.dispatchEvent(new Event('eip6963:requestProvider'))
}

export function subscribeToEvmWalletAnnouncements(
  listener: () => void
): () => void {
  initializeEIP6963Discovery()
  providerAnnouncementListeners.add(listener)
  return () => providerAnnouncementListeners.delete(listener)
}

function getLegacyInjectedEvmProvider(): EthereumProvider | null {
  if (window.phantom?.ethereum) {
    return window.phantom.ethereum
  }

  if (window.ethereum?.providers?.length) {
    return (
      window.ethereum.providers.find((provider) => (
        provider.isMetaMask ||
        provider.isBinance ||
        provider.isPhantom ||
        provider.isTrust ||
        provider.isTrustWallet
      )) ?? window.ethereum.providers[0]
    )
  }

  return window.ethereum ?? window.trustwallet ?? null
}

function getLegacyInjectedEvmProviders(): EIP6963ProviderDetail[] {
  if (typeof window === 'undefined') return []

  const candidates: Array<{ name: string; rdns: string; provider: EthereumProvider | undefined }> = []
  if (window.phantom?.ethereum) {
    candidates.push({ name: 'Phantom', rdns: 'app.phantom', provider: window.phantom.ethereum })
  }
  if (window.ethereum?.providers?.length) {
    window.ethereum.providers.forEach((provider, index) => {
      candidates.push({
        name: provider.isMetaMask ? 'MetaMask' : provider.isBinance ? 'Binance Wallet' : provider.isPhantom ? 'Phantom' : provider.isTrust || provider.isTrustWallet ? 'Trust Wallet' : `Wallet ${index + 1}`,
        rdns: provider.isMetaMask ? 'io.metamask' : provider.isBinance ? 'com.binance.wallet' : provider.isPhantom ? 'app.phantom' : provider.isTrust || provider.isTrustWallet ? 'com.trustwallet.app' : `legacy.wallet.${index + 1}`,
        provider,
      })
    })
  } else if (window.ethereum) {
    candidates.push({ name: window.ethereum.isMetaMask ? 'MetaMask' : 'EVM Wallet', rdns: window.ethereum.isMetaMask ? 'io.metamask' : 'legacy.ethereum', provider: window.ethereum })
  }
  if (window.trustwallet) {
    candidates.push({ name: 'Trust Wallet', rdns: 'com.trustwallet.app', provider: window.trustwallet })
  }

  const seen = new Set<EthereumProvider>()
  return candidates.flatMap(({ name, rdns, provider }) => {
    if (!provider || seen.has(provider)) return []
    seen.add(provider)
    return [{ info: { uuid: `legacy-${rdns}`, name, rdns }, provider }]
  })
}

export async function discoverEvmWallets(): Promise<EvmWalletOption[]> {
  initializeEIP6963Discovery()
  await new Promise((resolve) => setTimeout(resolve, 100))

  const details = [...Array.from(announcedProviders.values()), ...getLegacyInjectedEvmProviders()]
  return buildEvmWalletOptions(details)
}

export function buildEvmWalletOptions(
  details: EIP6963ProviderDetail[]
): EvmWalletOption[] {
  const seen = new Set<EthereumProvider>()
  return details.flatMap(({ info, provider }) => {
    if (seen.has(provider)) return []
    seen.add(provider)
    return [{ id: info.uuid, name: info.name, provider }]
  })
}

export function selectEvmProvider(
  eip6963Providers: EIP6963ProviderDetail[],
  legacyProvider: EthereumProvider | null
): EthereumProvider | null {
  if (legacyProvider) {
    return (
      eip6963Providers.find(({ provider }) => provider === legacyProvider)?.provider ??
      legacyProvider
    )
  }

  if (eip6963Providers.length === 1) {
    return eip6963Providers[0].provider
  }

  return (
    eip6963Providers.find(({ info }) => info.rdns === 'com.trustwallet.app')?.provider ??
    eip6963Providers[0]?.provider ??
    null
  )
}

export function getInjectedEvmProvider(): EthereumProvider | null {
  if (typeof window === 'undefined') return null

  initializeEIP6963Discovery()
  return selectEvmProvider(
    Array.from(announcedProviders.values()),
    getLegacyInjectedEvmProvider()
  )
}

initializeEIP6963Discovery()
