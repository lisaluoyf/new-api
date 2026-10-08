import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'

// Only an explicit completed refund clears the displayed charge. A failed
// task alone does not prove that funds have already been returned.
export function isRefundedConsumeLog(
  log: Pick<UsageLog, 'type'>,
  other: LogOtherData | null
): boolean {
  return log.type === 2 && other?.billing_refunded === true
}
