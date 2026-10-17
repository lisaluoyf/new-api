import type { ProviderChannel } from '../api'

export const CHANNEL_CATEGORIES = ['default', 'cloud', 'official'] as const
export type ChannelCategory = (typeof CHANNEL_CATEGORIES)[number]

export function channelSelected(
  channel: ProviderChannel,
  ids: number[],
  categories: ChannelCategory[]
): boolean {
  return (
    ids.includes(channel.channel_id) ||
    categories.includes(channel.channel_category ?? 'default')
  )
}

export function availableChannelCount(
  channels: ProviderChannel[],
  ids: number[],
  categories: ChannelCategory[],
  mode: 'include' | 'exclude'
): number {
  return new Set(
    channels
      .filter(
        (channel) =>
          channel.status === 1 &&
          channelSelected(channel, ids, categories) === (mode === 'include')
      )
      .map((channel) => channel.channel_id)
  ).size
}
