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
import { useState, useCallback, useRef } from 'react'
import { toast } from 'sonner'
import i18next from 'i18next'
import {
  createCryptoDepositIntent,
  submitCryptoDeposit,
  getCryptoDepositStatus,
} from '../api'
import {
  getInjectedEvmProvider,
  type EthereumProvider,
} from '../lib/evm-provider'

// ============================================================================
// Chain / Token Configuration
// ============================================================================

export interface TokenConfig {
  symbol: string
  address: string | null  // null = native coin
  decimals: number
  isNative: boolean
}

export interface ChainConfig {
  id: string
  name: string
  shortLabel: string   // chip 上显示的简称
  chainId: number
  chainIdHex: string
  tokens: TokenConfig[]
}

export const CHAINS: ChainConfig[] = [
  {
    id: 'eth',
    name: 'Ethereum',
    shortLabel: 'ETH',
    chainId: 1,
    chainIdHex: '0x1',
    tokens: [
      { symbol: 'ETH',  address: null, decimals: 18, isNative: true },
      { symbol: 'USDT', address: '0xdAC17F958D2ee523a2206206994597C13D831ec7', decimals: 6,  isNative: false },
      { symbol: 'USDC', address: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48', decimals: 6,  isNative: false },
    ],
  },
  {
    id: 'bsc',
    name: 'BNB Smart Chain',
    shortLabel: 'BNB',
    chainId: 56,
    chainIdHex: '0x38',
    tokens: [
      { symbol: 'BNB',  address: null, decimals: 18, isNative: true },
      { symbol: 'USDT', address: '0x55d398326f99059fF775485246999027B3197955', decimals: 18, isNative: false },
      { symbol: 'USDC', address: '0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d', decimals: 18, isNative: false },
    ],
  },
  {
    id: 'polygon',
    name: 'Polygon',
    shortLabel: 'POL',
    chainId: 137,
    chainIdHex: '0x89',
    tokens: [
      { symbol: 'POL',  address: null, decimals: 18, isNative: true },
      { symbol: 'USDT', address: '0xc2132D05D31c914a87C6611C10748AEb04B58e8F', decimals: 6,  isNative: false },
      { symbol: 'USDC', address: '0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174', decimals: 6,  isNative: false },
    ],
  },
  {
    id: 'arbitrum',
    name: 'Arbitrum One',
    shortLabel: 'ARB',
    chainId: 42161,
    chainIdHex: '0xa4b1',
    tokens: [
      { symbol: 'ETH',  address: null, decimals: 18, isNative: true },
      { symbol: 'USDT', address: '0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9', decimals: 6,  isNative: false },
      { symbol: 'USDC', address: '0xaf88d065e77c8cC2239327C5EDb3A432268e5831', decimals: 6,  isNative: false },
    ],
  },
  {
    id: 'base',
    name: 'Base',
    shortLabel: 'BASE',
    chainId: 8453,
    chainIdHex: '0x2105',
    tokens: [
      { symbol: 'ETH',  address: null, decimals: 18, isNative: true },
      { symbol: 'USDC', address: '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913', decimals: 6,  isNative: false },
    ],
  },
]

const PLATFORM_WALLET = '0x33de43dad6955655ec0543f32069ac331e633c9c'

// ============================================================================
// Encoding helpers
// ============================================================================

function encodeErc20Transfer(to: string, amount: bigint): string {
  const selector = 'a9059cbb'
  const toHex = to.toLowerCase().replace('0x', '').padStart(64, '0')
  const amountHex = amount.toString(16).padStart(64, '0')
  return '0x' + selector + toHex + amountHex
}

function parseTokenAmount(usdAmount: number, decimals: number): bigint {
  const scaled6 = BigInt(Math.round(usdAmount * 1_000_000))
  if (decimals <= 6) return scaled6 / BigInt(10 ** (6 - decimals))
  return scaled6 * BigInt(10 ** (decimals - 6))
}

function utf8ToHex(value: string): string {
  const bytes = new TextEncoder().encode(value)
  return '0x' + Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}

// For native coins we need real token amount from the user's wallet perspective.
// Since the user pays in native coin but we price in USD, we calculate the
// native amount server-side after verifying the tx. On the frontend we just
// send value=0x0 placeholder and let the wallet show the native amount
// … actually we CAN'T know the native price client-side without an oracle.
// Strategy: open the tx with value=0 and let the user enter native amount manually?
// No – better approach: fetch price from CoinGecko public API.
export async function fetchNativePrice(coingeckoId: string): Promise<number> {
  try {
    const res = await fetch(
      `https://api.coingecko.com/api/v3/simple/price?ids=${coingeckoId}&vs_currencies=usd`,
      { signal: AbortSignal.timeout(5000) }
    )
    const json = await res.json()
    return json[coingeckoId]?.usd ?? 0
  } catch {
    return 0
  }
}

export const NATIVE_COINGECKO: Record<string, string> = {
  eth:      'ethereum',
  bsc:      'binancecoin',
  polygon:  'matic-network',
  arbitrum: 'ethereum',  // ARB One native = ETH
  base:     'ethereum',  // Base native = ETH
}

// ============================================================================
// Types
// ============================================================================

export type CryptoStep =
  | 'form'
  | 'connecting'
  | 'switching'
  | 'signing'
  | 'confirming'
  | 'processing'
  | 'done'
  | 'failed'

export interface UseCryptoPaymentReturn {
  step: CryptoStep
  error: string | null
  txHash: string | null
  usdAdded: number
  walletAddress: string | null
  nativePrice: number
  startPayment: (amount: number, chain: ChainConfig, token: TokenConfig) => Promise<void>
  reset: () => void
}

// ============================================================================
// Hook
// ============================================================================

function isPendingWalletRequest(error: unknown): boolean {
  const code = (error as { code?: number })?.code
  const message = typeof (error as { message?: unknown })?.message === 'string'
    ? (error as { message: string }).message.toLowerCase()
    : ''

  return (
    code === -32002 ||
    message.includes('already pending') ||
    message.includes('unlockpopup')
  )
}

function getProviderErrorMessage(error: unknown): string | null {
  if (error instanceof Error && error.message) {
    return error.message
  }

  if (typeof error === 'string' && error.trim()) {
    return error
  }

  const message = (error as { message?: unknown })?.message
  if (typeof message === 'string' && message.trim()) {
    return message
  }

  const shortMessage = (error as { shortMessage?: unknown })?.shortMessage
  if (typeof shortMessage === 'string' && shortMessage.trim()) {
    return shortMessage
  }

  return null
}

function isUnsupportedPhantomChain(provider: EthereumProvider, chain: ChainConfig): boolean {
  return provider.isPhantom === true && chain.id === 'bsc'
}

async function signWalletChallenge(
  provider: EthereumProvider,
  challenge: string,
  from: string
): Promise<string> {
  const payload = utf8ToHex(challenge)
  try {
    return (await provider.request({
      method: 'personal_sign',
      params: [payload, from],
    })) as string
  } catch (error) {
    const message = getProviderErrorMessage(error)?.toLowerCase() ?? ''
    if (
      message.includes('invalid parameters') ||
      message.includes('invalid param') ||
      message.includes('expected') ||
      message.includes('unsupported')
    ) {
      return (await provider.request({
        method: 'personal_sign',
        params: [from, payload],
      })) as string
    }
    throw error
  }
}

export function useCryptoPayment(): UseCryptoPaymentReturn {
  const [step, setStep] = useState<CryptoStep>('form')
  const [error, setError] = useState<string | null>(null)
  const [txHash, setTxHash] = useState<string | null>(null)
  const [usdAdded, setUsdAdded] = useState(0)
  const [walletAddress, setWalletAddress] = useState<string | null>(null)
  const [nativePrice, setNativePrice] = useState(0)
  const pollTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const paymentLockRef = useRef(false)

  const reset = useCallback(() => {
    if (pollTimer.current) clearTimeout(pollTimer.current)
    pollTimer.current = null
    setStep('form')
    setError(null)
    setTxHash(null)
    setUsdAdded(0)
    setWalletAddress(null)
    setNativePrice(0)
    paymentLockRef.current = false
  }, [])

  const startPayment = useCallback(
    async (amount: number, chain: ChainConfig, token: TokenConfig) => {
      if (paymentLockRef.current) {
        return
      }

      const provider = getInjectedEvmProvider()
      if (!provider) {
        setError(i18next.t('No wallet detected. Please install MetaMask or Binance Wallet.'))
        setStep('failed')
        return
      }

      paymentLockRef.current = true

      try {
        if (isUnsupportedPhantomChain(provider, chain)) {
          throw new Error(
            i18next.t('Phantom does not support BNB Smart Chain deposits yet. Please switch to ETH, Polygon, Arbitrum, or Base, or use another EVM wallet.')
          )
        }

        // 1. 连接钱包
        setError(null)
        setStep('connecting')
        const accounts = (await provider.request({
          method: 'eth_requestAccounts',
        })) as string[]
        const from = accounts[0]
        if (!from) throw new Error('No account selected')
        setWalletAddress(from)

        // 2. 切链
        setStep('switching')
        try {
          await provider.request({
            method: 'wallet_switchEthereumChain',
            params: [{ chainId: chain.chainIdHex }],
          })
        } catch (switchErr: unknown) {
          const err = switchErr as { code?: number }
          if (err.code !== 4902) throw switchErr
          throw new Error(
            i18next.t('Chain not configured in your wallet. Please add {{chain}} manually.', {
              chain: chain.name,
            }),
            { cause: switchErr }
          )
        }

        const intentRes = await createCryptoDepositIntent(chain.id, token.symbol, from)
        if (!intentRes.success || !intentRes.depositId || !intentRes.challenge) {
          throw new Error(intentRes.error ?? i18next.t('Failed to create crypto deposit intent'))
        }
        const depositId = intentRes.depositId
        const depositAddress = intentRes.toAddress ?? PLATFORM_WALLET
        setStep('signing')
        const walletSignature = await signWalletChallenge(provider, intentRes.challenge, from)

        // 3. 构建并发送交易
        setStep('confirming')
        let hash: string

        if (token.isNative) {
          // 原生币：先查价格算 wei 数量
          const cgId = NATIVE_COINGECKO[chain.id] ?? 'ethereum'
          const price = await fetchNativePrice(cgId)
          setNativePrice(price)
          if (price <= 0) throw new Error(i18next.t('Failed to fetch coin price'))

          const nativeAmount = amount / price  // e.g. $50 / $3000 = 0.01667 ETH
          const weiAmount = BigInt(Math.round(nativeAmount * 1e9)) * BigInt(1e9) // avoid float precision
          const valueHex = '0x' + weiAmount.toString(16)

          hash = (await provider.request({
            method: 'eth_sendTransaction',
            params: [{ from, to: depositAddress, value: valueHex, data: '0x' }],
          })) as string
        } else {
          // ERC-20
          const tokenAmount = parseTokenAmount(amount, token.decimals)
          const data = encodeErc20Transfer(depositAddress, tokenAmount)
          hash = (await provider.request({
            method: 'eth_sendTransaction',
            params: [{ from, to: token.address, data, value: '0x0' }],
          })) as string
        }

        setTxHash(hash)

        // 4. 提交后端轮询
        setStep('processing')
        const submitRes = await submitCryptoDeposit(depositId, hash, walletSignature)
        if (!submitRes.success || !submitRes.depositId) {
          throw new Error(submitRes.error ?? i18next.t('Failed to submit transaction'))
        }

        const pollStartedAt = Date.now()
        const maxWaitMs = 15 * 60 * 1000
        const pollIntervalMs = 3000

        await new Promise<void>((resolve, reject) => {
          const stopPolling = () => {
            if (pollTimer.current) {
              clearTimeout(pollTimer.current)
              pollTimer.current = null
            }
          }

          const scheduleNextPoll = () => {
            pollTimer.current = setTimeout(() => {
              void pollOnce()
            }, pollIntervalMs)
          }

          const pollOnce = async () => {
            if (Date.now() - pollStartedAt >= maxWaitMs) {
              stopPolling()
              reject(new Error(i18next.t('Timed out waiting for confirmation')))
              return
            }

            try {
              const status = await getCryptoDepositStatus(depositId)
              if (status.status === 'confirmed') {
                stopPolling()
                setUsdAdded(status.usdAdded ?? 0)
                setStep('done')
                resolve()
                return
              }
              if (status.status === 'failed') {
                stopPolling()
                reject(new Error(i18next.t('Transaction verification failed')))
                return
              }
            } catch {
              // Transient query failures should not turn into a hard fail while
              // the chain confirmation is still in flight.
            }

            scheduleNextPoll()
          }

          void pollOnce()
        })
      } catch (err: unknown) {
        const msg = getProviderErrorMessage(err) ?? i18next.t('Payment failed')
        if ((err as { code?: number }).code === 4001) {
          toast.info(i18next.t('Transaction cancelled'))
          setStep('form')
          return
        }
        setError(
          isPendingWalletRequest(err)
            ? i18next.t('A wallet request is already pending. Please open your wallet extension and finish or cancel it before trying again.')
            : msg
        )
        setStep('failed')
      } finally {
        paymentLockRef.current = false
      }
    },
    []
  )

  return { step, error, txHash, usdAdded, walletAddress, nativePrice, startPayment, reset }
}
