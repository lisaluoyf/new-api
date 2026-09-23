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
import { useState, useCallback, useEffect, useRef } from 'react'
import {
  createAssociatedTokenAccountIdempotentInstruction,
  createTransferCheckedInstruction,
  getAssociatedTokenAddressSync,
} from '@solana/spl-token'
import {
  Connection,
  PublicKey,
  SystemProgram,
  Transaction,
} from '@solana/web3.js'
import i18next from 'i18next'
import { toast } from 'sonner'
import {
  authorizeCryptoDepositIntent,
  cancelCryptoDepositIntent,
  createCryptoDepositIntent,
  submitCryptoDeposit,
  getCryptoDepositStatus,
} from '../api'
import type {
  CryptoChainFamily,
  CryptoWalletProvider,
} from '../lib/crypto-wallet-provider'
import type { EthereumProvider } from '../lib/evm-provider'

// ============================================================================
// Chain / Token Configuration
// ============================================================================

export interface TokenConfig {
  symbol: string
  address: string | null // null = native coin
  decimals: number
  isNative: boolean
}

export interface ChainConfig {
  id: string
  name: string
  shortLabel: string // chip 上显示的简称
  family: CryptoChainFamily
  chainId?: number
  chainIdHex?: string
  tokens: TokenConfig[]
}

export const CHAINS: ChainConfig[] = [
  {
    id: 'eth',
    family: 'evm',
    name: 'Ethereum',
    shortLabel: 'ETH',
    chainId: 1,
    chainIdHex: '0x1',
    tokens: [
      { symbol: 'ETH', address: null, decimals: 18, isNative: true },
      {
        symbol: 'USDT',
        address: '0xdAC17F958D2ee523a2206206994597C13D831ec7',
        decimals: 6,
        isNative: false,
      },
      {
        symbol: 'USDC',
        address: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
        decimals: 6,
        isNative: false,
      },
    ],
  },
  {
    id: 'bsc',
    family: 'evm',
    name: 'BNB Smart Chain',
    shortLabel: 'BNB',
    chainId: 56,
    chainIdHex: '0x38',
    tokens: [
      { symbol: 'BNB', address: null, decimals: 18, isNative: true },
      {
        symbol: 'USDT',
        address: '0x55d398326f99059fF775485246999027B3197955',
        decimals: 18,
        isNative: false,
      },
      {
        symbol: 'USDC',
        address: '0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d',
        decimals: 18,
        isNative: false,
      },
    ],
  },
  {
    id: 'polygon',
    family: 'evm',
    name: 'Polygon',
    shortLabel: 'POL',
    chainId: 137,
    chainIdHex: '0x89',
    tokens: [
      { symbol: 'POL', address: null, decimals: 18, isNative: true },
      {
        symbol: 'USDT',
        address: '0xc2132D05D31c914a87C6611C10748AEb04B58e8F',
        decimals: 6,
        isNative: false,
      },
      {
        symbol: 'USDC',
        address: '0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174',
        decimals: 6,
        isNative: false,
      },
    ],
  },
  {
    id: 'arbitrum',
    family: 'evm',
    name: 'Arbitrum One',
    shortLabel: 'ARB',
    chainId: 42161,
    chainIdHex: '0xa4b1',
    tokens: [
      { symbol: 'ETH', address: null, decimals: 18, isNative: true },
      {
        symbol: 'USDT',
        address: '0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9',
        decimals: 6,
        isNative: false,
      },
      {
        symbol: 'USDC',
        address: '0xaf88d065e77c8cC2239327C5EDb3A432268e5831',
        decimals: 6,
        isNative: false,
      },
    ],
  },
  {
    id: 'base',
    family: 'evm',
    name: 'Base',
    shortLabel: 'BASE',
    chainId: 8453,
    chainIdHex: '0x2105',
    tokens: [
      { symbol: 'ETH', address: null, decimals: 18, isNative: true },
      {
        symbol: 'USDC',
        address: '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913',
        decimals: 6,
        isNative: false,
      },
    ],
  },
  {
    id: 'tron',
    family: 'tron',
    name: 'TRON',
    shortLabel: 'TRX',
    tokens: [
      { symbol: 'TRX', address: null, decimals: 6, isNative: true },
      {
        symbol: 'USDT',
        address: 'TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj',
        decimals: 6,
        isNative: false,
      },
    ],
  },
  {
    id: 'solana',
    family: 'solana',
    name: 'Solana',
    shortLabel: 'SOL',
    tokens: [
      { symbol: 'SOL', address: null, decimals: 9, isNative: true },
      {
        symbol: 'USDT',
        address: 'Es9vMFrzaCERmJfrF4H2FYDq9wHnbWkqHqrjL4FhMuJ',
        decimals: 6,
        isNative: false,
      },
    ],
  },
]

const PLATFORM_WALLET = '0x33de43dad6955655ec0543f32069ac331e633c9c'
const PENDING_CRYPTO_DEPOSIT_KEY = 'apimaster_pending_crypto_deposit'

interface PendingCryptoDeposit {
  depositId: string
  createdAt: number
}

function readPendingCryptoDeposit(): PendingCryptoDeposit | null {
  try {
    const pending = JSON.parse(
      localStorage.getItem(PENDING_CRYPTO_DEPOSIT_KEY) ?? 'null'
    ) as PendingCryptoDeposit | null
    if (
      !pending?.depositId ||
      !pending.createdAt ||
      Date.now() - pending.createdAt >= 30 * 60 * 1000
    ) {
      localStorage.removeItem(PENDING_CRYPTO_DEPOSIT_KEY)
      return null
    }
    return pending
  } catch {
    localStorage.removeItem(PENDING_CRYPTO_DEPOSIT_KEY)
    return null
  }
}

function savePendingCryptoDeposit(depositId: string) {
  const pending: PendingCryptoDeposit = { depositId, createdAt: Date.now() }
  localStorage.setItem(PENDING_CRYPTO_DEPOSIT_KEY, JSON.stringify(pending))
}

function clearPendingCryptoDeposit(depositId: string) {
  const pending = readPendingCryptoDeposit()
  if (pending?.depositId === depositId) {
    localStorage.removeItem(PENDING_CRYPTO_DEPOSIT_KEY)
  }
}

// ============================================================================
// Encoding helpers
// ============================================================================

function encodeErc20Transfer(to: string, amount: bigint): string {
  const selector = 'a9059cbb'
  const toHex = to.toLowerCase().replace('0x', '').padStart(64, '0')
  const amountHex = amount.toString(16).padStart(64, '0')
  return '0x' + selector + toHex + amountHex
}

function bytesToBase64(value: Uint8Array): string {
  let binary = ''
  value.forEach((byte) => {
    binary += String.fromCharCode(byte)
  })
  return window.btoa(binary)
}

function utf8ToHex(value: string): string {
  const bytes = new TextEncoder().encode(value)
  return (
    '0x' +
    Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  )
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
  eth: 'ethereum',
  bsc: 'binancecoin',
  polygon: 'matic-network',
  arbitrum: 'ethereum', // ARB One native = ETH
  base: 'ethereum', // Base native = ETH
  tron: 'tron',
  solana: 'solana',
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
  startPayment: (
    amount: number,
    chain: ChainConfig,
    token: TokenConfig,
    selectedProvider: CryptoWalletProvider
  ) => Promise<void>
  reset: () => void
  detach: () => void
}

// ============================================================================
// Hook
// ============================================================================

function isPendingWalletRequest(error: unknown): boolean {
  const code = (error as { code?: number })?.code
  const message =
    typeof (error as { message?: unknown })?.message === 'string'
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

function isUnsupportedPhantomChain(
  provider: EthereumProvider,
  chain: ChainConfig
): boolean {
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
  const [pendingDepositId, setPendingDepositId] = useState<string | null>(null)
  const paymentLockRef = useRef(false)
  const paymentAttemptRef = useRef(0)
  const presentPendingConfirmationRef = useRef(false)

  useEffect(() => {
    const pending = readPendingCryptoDeposit()
    if (!pending) return
    setPendingDepositId(pending.depositId)
  }, [])

  useEffect(() => {
    if (!pendingDepositId) return

    let cancelled = false
    let pollTimer: ReturnType<typeof setTimeout> | null = null

    const poll = async () => {
      try {
        const status = await getCryptoDepositStatus(pendingDepositId)
        if (cancelled) return
        if (status.status === 'confirmed') {
          clearPendingCryptoDeposit(pendingDepositId)
          if (presentPendingConfirmationRef.current) {
            setUsdAdded(status.usdAdded ?? 0)
            setStep('done')
          }
          setPendingDepositId(null)
          paymentLockRef.current = false
          return
        }
        if (status.status === 'failed' || status.status === 'expired') {
          clearPendingCryptoDeposit(pendingDepositId)
          if (presentPendingConfirmationRef.current) {
            setStep('failed')
          }
          setPendingDepositId(null)
          paymentLockRef.current = false
          return
        }
      } catch {
        // Status failures are transient; the server owns final settlement.
      }
      if (!cancelled) {
        pollTimer = setTimeout(() => void poll(), 3000)
      }
    }

    void poll()
    return () => {
      cancelled = true
      if (pollTimer) clearTimeout(pollTimer)
    }
  }, [pendingDepositId])

  const reset = useCallback(() => {
    setStep('form')
    setError(null)
    setTxHash(null)
    setUsdAdded(0)
    setWalletAddress(null)
    setNativePrice(0)
    paymentLockRef.current = false
    presentPendingConfirmationRef.current = false
  }, [])

  const detach = useCallback(() => {
    paymentAttemptRef.current += 1
    const pending = readPendingCryptoDeposit()
    if (pending) clearPendingCryptoDeposit(pending.depositId)
    setPendingDepositId(null)
    reset()
  }, [reset])

  const startPayment = useCallback(
    async (
      amount: number,
      chain: ChainConfig,
      token: TokenConfig,
      selectedProvider: CryptoWalletProvider
    ) => {
      if (paymentLockRef.current) {
        return
      }

      paymentLockRef.current = true
      const attemptId = paymentAttemptRef.current + 1
      paymentAttemptRef.current = attemptId
      const isCurrentAttempt = () => paymentAttemptRef.current === attemptId
      const setCurrentStep = (nextStep: CryptoStep) => {
        if (isCurrentAttempt()) setStep(nextStep)
      }
      const setCurrentError = (nextError: string | null) => {
        if (isCurrentAttempt()) setError(nextError)
      }
      let currentDepositId = ''
      let paymentAuthorized = false

      try {
        setCurrentError(null)
        setCurrentStep('connecting')
        let from = ''
        if (selectedProvider.family === 'evm') {
          const accounts = (await selectedProvider.ethereum.request({
            method: 'eth_requestAccounts',
          })) as string[]
          from = accounts[0] ?? ''
        } else if (selectedProvider.family === 'tron') {
          await selectedProvider.tronLink.request?.({
            method: 'tron_requestAccounts',
          })
          from = selectedProvider.tronWeb.defaultAddress?.base58 ?? ''
        } else {
          const connected = await selectedProvider.solana.connect()
          from =
            connected.publicKey?.toBase58() ??
            selectedProvider.solana.publicKey?.toBase58() ??
            ''
        }
        if (!from) throw new Error('No account selected')
        if (isCurrentAttempt()) setWalletAddress(from)

        if (selectedProvider.family === 'evm') {
          if (isUnsupportedPhantomChain(selectedProvider.ethereum, chain)) {
            throw new Error(
              i18next.t(
                'Phantom does not support BNB Smart Chain deposits yet. Please switch to ETH, Polygon, Arbitrum, or Base, or use another EVM wallet.'
              )
            )
          }
          setCurrentStep('switching')
          try {
            await selectedProvider.ethereum.request({
              method: 'wallet_switchEthereumChain',
              params: [{ chainId: chain.chainIdHex }],
            })
          } catch (switchErr: unknown) {
            const walletError = switchErr as { code?: number }
            if (walletError.code !== 4902) throw switchErr
            throw new Error(
              i18next.t(
                'Chain not configured in your wallet. Please add {{chain}} manually.',
                { chain: chain.name }
              ),
              { cause: switchErr }
            )
          }
        }

        const intentRes = await createCryptoDepositIntent(
          chain.id,
          token.symbol,
          from,
          amount
        )
        if (
          !intentRes.success ||
          !intentRes.depositId ||
          !intentRes.challenge
        ) {
          throw new Error(
            intentRes.error ??
              i18next.t('Failed to create crypto deposit intent')
          )
        }
        const depositId = intentRes.depositId
        currentDepositId = depositId
        const depositAddress =
          intentRes.toAddress ??
          (selectedProvider.family === 'evm' ? PLATFORM_WALLET : '')
        if (!depositAddress)
          throw new Error(i18next.t('Crypto recipient is not configured'))
        setCurrentStep('signing')
        let walletSignature = ''
        if (selectedProvider.family === 'evm') {
          walletSignature = await signWalletChallenge(
            selectedProvider.ethereum,
            intentRes.challenge,
            from
          )
        } else if (selectedProvider.family === 'tron') {
          walletSignature = await selectedProvider.tronWeb.trx.signMessageV2(
            intentRes.challenge
          )
        } else {
          const signed = await selectedProvider.solana.signMessage(
            new TextEncoder().encode(intentRes.challenge)
          )
          walletSignature = bytesToBase64(signed.signature)
        }

        const authorization = await authorizeCryptoDepositIntent(
          depositId,
          walletSignature
        )
        if (!authorization.success) {
          throw new Error(
            authorization.error ??
              i18next.t('Failed to create crypto deposit intent')
          )
        }
        paymentAuthorized = true
        if (!intentRes.baseUnitAmount) {
          throw new Error(i18next.t('Failed to quote native asset'))
        }
        if (isCurrentAttempt()) {
          savePendingCryptoDeposit(depositId)
          setPendingDepositId(depositId)
          presentPendingConfirmationRef.current = true
        }
        const baseUnitAmount = BigInt(intentRes.baseUnitAmount)

        setCurrentStep('confirming')
        let hash: string

        if (selectedProvider.family === 'evm') {
          if (token.isNative) {
            if (!intentRes.nativeAssetAmount)
              throw new Error(i18next.t('Failed to quote native asset'))
            if (isCurrentAttempt())
              setNativePrice(intentRes.assetUsdPrice ?? 0)
            const value = baseUnitAmount
            hash = (await selectedProvider.ethereum.request({
              method: 'eth_sendTransaction',
              params: [
                {
                  from,
                  to: depositAddress,
                  value: `0x${value.toString(16)}`,
                  data: '0x',
                },
              ],
            })) as string
          } else {
            const data = encodeErc20Transfer(depositAddress, baseUnitAmount)
            hash = (await selectedProvider.ethereum.request({
              method: 'eth_sendTransaction',
              params: [{ from, to: token.address, data, value: '0x0' }],
            })) as string
          }
        } else if (selectedProvider.family === 'tron') {
          let transaction: unknown
          if (token.isNative) {
            if (!intentRes.nativeAssetAmount)
              throw new Error(i18next.t('Failed to quote native asset'))
            if (isCurrentAttempt())
              setNativePrice(intentRes.assetUsdPrice ?? 0)
            transaction =
              await selectedProvider.tronWeb.transactionBuilder.sendTrx(
                depositAddress,
                Number(baseUnitAmount),
                from
              )
          } else {
            const triggered =
              await selectedProvider.tronWeb.transactionBuilder.triggerSmartContract(
                token.address!,
                'transfer(address,uint256)',
                { feeLimit: 100_000_000 },
                [
                  { type: 'address', value: depositAddress },
                  {
                    type: 'uint256',
                    value: baseUnitAmount.toString(),
                  },
                ],
                from
              )
            if (!triggered.result?.result || !triggered.transaction)
              throw new Error(i18next.t('Failed to build transaction'))
            transaction = triggered.transaction
          }
          const signedTransaction =
            await selectedProvider.tronWeb.trx.sign(transaction)
          const broadcast =
            await selectedProvider.tronWeb.trx.sendRawTransaction(
              signedTransaction
            )
          if (!broadcast.result || !broadcast.txid)
            throw new Error(
              broadcast.code ?? i18next.t('Failed to submit transaction')
            )
          hash = broadcast.txid
        } else {
          const connection = new Connection(
            'https://api.mainnet-beta.solana.com',
            'confirmed'
          )
          const fromKey = new PublicKey(from)
          const toKey = new PublicKey(depositAddress)
          const transaction = new Transaction()
          if (token.isNative) {
            if (!intentRes.nativeAssetAmount)
              throw new Error(i18next.t('Failed to quote native asset'))
            if (isCurrentAttempt())
              setNativePrice(intentRes.assetUsdPrice ?? 0)
            transaction.add(
              SystemProgram.transfer({
                fromPubkey: fromKey,
                toPubkey: toKey,
                lamports: baseUnitAmount,
              })
            )
          } else {
            const mint = new PublicKey(token.address!)
            const sourceAccount = getAssociatedTokenAddressSync(mint, fromKey)
            const destinationAccount = getAssociatedTokenAddressSync(
              mint,
              toKey
            )
            transaction.add(
              createAssociatedTokenAccountIdempotentInstruction(
                fromKey,
                destinationAccount,
                toKey,
                mint
              ),
              createTransferCheckedInstruction(
                sourceAccount,
                mint,
                destinationAccount,
                fromKey,
                baseUnitAmount,
                token.decimals
              )
            )
          }
          const latestBlockhash =
            await connection.getLatestBlockhash('finalized')
          transaction.feePayer = fromKey
          transaction.recentBlockhash = latestBlockhash.blockhash
          const sent =
            await selectedProvider.solana.signAndSendTransaction(transaction)
          hash = typeof sent === 'string' ? sent : sent.signature
        }

        if (isCurrentAttempt()) setTxHash(hash)

        setCurrentStep('processing')
        const submitResult = await submitCryptoDeposit(depositId, hash)
        if (!submitResult.success) {
          setCurrentError(
            submitResult.error ?? i18next.t('Failed to submit transaction')
          )
        }
      } catch (err: unknown) {
        const msg = getProviderErrorMessage(err) ?? i18next.t('Payment failed')
        if ((err as { code?: number }).code === 4001) {
          if (currentDepositId && !paymentAuthorized) {
            const cancellation =
              await cancelCryptoDepositIntent(currentDepositId)
            if (!cancellation.success) {
              setCurrentError(msg)
              setCurrentStep('processing')
              return
            }
            if (cancellation.success) {
              clearPendingCryptoDeposit(currentDepositId)
              if (isCurrentAttempt()) setPendingDepositId(null)
            }
          }
          if (isCurrentAttempt()) {
            toast.info(i18next.t('Transaction cancelled'))
          }
          setCurrentStep('form')
          return
        }
        if (currentDepositId && paymentAuthorized) {
          setCurrentError(msg)
          setCurrentStep('processing')
          return
        }
        if (currentDepositId && !paymentAuthorized) {
          void cancelCryptoDepositIntent(currentDepositId)
        }
        setCurrentError(
          isPendingWalletRequest(err)
            ? i18next.t(
                'A wallet request is already pending. Please open your wallet extension and finish or cancel it before trying again.'
              )
            : msg
        )
        setCurrentStep('failed')
      } finally {
        if (isCurrentAttempt()) paymentLockRef.current = false
      }
    },
    []
  )

  return {
    step,
    error,
    txHash,
    usdAdded,
    walletAddress,
    nativePrice,
    startPayment,
    reset,
    detach,
  }
}
