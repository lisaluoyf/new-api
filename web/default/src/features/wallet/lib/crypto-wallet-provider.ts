/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  discoverEvmWallets,
  subscribeToEvmWalletAnnouncements,
  type EthereumProvider,
} from './evm-provider'

export type CryptoChainFamily = 'evm' | 'tron' | 'solana'

export interface TronProvider {
  request?: (args: { method: string; params?: unknown[] }) => Promise<unknown>
  tronWeb?: TronWebLike
}

export interface TronWebLike {
  defaultAddress?: { base58?: string }
  transactionBuilder: {
    sendTrx: (to: string, amount: number, from: string) => Promise<unknown>
    triggerSmartContract: (
      contractAddress: string,
      functionSelector: string,
      options: Record<string, unknown>,
      parameters: Array<{ type: string; value: string }>,
      from: string
    ) => Promise<{ transaction?: unknown; result?: { result?: boolean } }>
  }
  trx: {
    sign: (transaction: unknown) => Promise<unknown>
    signMessageV2: (message: string) => Promise<string>
    sendRawTransaction: (
      transaction: unknown
    ) => Promise<{ result?: boolean; txid?: string; code?: string }>
  }
}

export interface SolanaProvider {
  isPhantom?: boolean
  isOkxWallet?: boolean
  publicKey?: { toBase58: () => string }
  connect: () => Promise<{ publicKey?: { toBase58: () => string } }>
  signMessage: (
    message: Uint8Array,
    display?: string
  ) => Promise<{ signature: Uint8Array }>
  signAndSendTransaction: (
    transaction: unknown
  ) => Promise<string | { signature: string }>
}

export type CryptoWalletProvider =
  | { family: 'evm'; ethereum: EthereumProvider }
  | { family: 'tron'; tronLink: TronProvider; tronWeb: TronWebLike }
  | { family: 'solana'; solana: SolanaProvider }

export interface CryptoWalletOption {
  id: string
  name: string
  provider: CryptoWalletProvider
}

declare global {
  interface Window {
    tronLink?: TronProvider
    tronWeb?: TronWebLike
    okxwallet?: {
      tronLink?: TronProvider
      tronWeb?: TronWebLike
      solana?: SolanaProvider
    }
  }
}

export async function discoverCryptoWallets(
  family: CryptoChainFamily
): Promise<CryptoWalletOption[]> {
  if (family === 'evm') {
    return (await discoverEvmWallets()).map((wallet) => ({
      id: wallet.id,
      name: wallet.name,
      provider: { family: 'evm', ethereum: wallet.provider },
    }))
  }

  if (typeof window === 'undefined') return []
  if (family === 'tron') {
    await new Promise((resolve) => setTimeout(resolve, 100))
    const wallets: CryptoWalletOption[] = []
    const tronLinkWeb = window.tronLink?.tronWeb ?? window.tronWeb
    if (window.tronLink && tronLinkWeb) {
      wallets.push({
        id: 'tronlink',
        name: 'TronLink',
        provider: {
          family: 'tron',
          tronLink: window.tronLink,
          tronWeb: tronLinkWeb,
        },
      })
    }
    if (
      window.okxwallet?.tronLink &&
      window.okxwallet.tronWeb &&
      window.okxwallet.tronWeb !== tronLinkWeb
    ) {
      wallets.push({
        id: 'okx-tron',
        name: 'OKX Wallet',
        provider: {
          family: 'tron',
          tronLink: window.okxwallet.tronLink,
          tronWeb: window.okxwallet.tronWeb,
        },
      })
    }
    return wallets
  }

  const wallets: CryptoWalletOption[] = []
  const phantom = (window as Window & { phantom?: { solana?: SolanaProvider } })
    .phantom
  if (phantom?.solana) {
    wallets.push({
      id: 'phantom-solana',
      name: 'Phantom',
      provider: { family: 'solana', solana: phantom.solana },
    })
  }
  if (window.okxwallet?.solana && window.okxwallet.solana !== phantom?.solana) {
    wallets.push({
      id: 'okx-solana',
      name: 'OKX Wallet',
      provider: { family: 'solana', solana: window.okxwallet.solana },
    })
  }
  return wallets
}

export function subscribeToCryptoWalletChanges(
  family: CryptoChainFamily,
  listener: () => void
): () => void {
  if (family === 'evm') {
    return subscribeToEvmWalletAnnouncements(listener)
  }
  return () => {}
}
