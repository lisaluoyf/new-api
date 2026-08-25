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
      }
    })
    discoveryInitialized = true
  }

  window.dispatchEvent(new Event('eip6963:requestProvider'))
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
