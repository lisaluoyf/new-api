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
import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { CopyButton } from '@/components/copy-button'
import { downloadMediaFile } from '../../lib/download-media'
import {
  loadAuthenticatedMediaUrl,
  MediaLoadError,
} from '../../lib/load-authenticated-media'
import { MediaDialogFooter } from './media-dialog-footer'
import { RequestDataPanel } from './request-data-panel'

interface VideoDialogProps {
  videoUrl: string
  fallbackUrl?: string
  taskId?: string
  requestData?: Record<string, unknown> | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface VideoMetadata {
  width: number
  height: number
  duration: number
}

interface LoadedVideoInfo {
  contentType?: string
  sizeBytes?: number
}

function greatestCommonDivisor(a: number, b: number): number {
  let x = Math.abs(Math.round(a))
  let y = Math.abs(Math.round(b))
  while (y > 0) {
    const remainder = x % y
    x = y
    y = remainder
  }
  return x || 1
}

function formatAspectRatio(width: number, height: number): string {
  if (width <= 0 || height <= 0) return ''
  const divisor = greatestCommonDivisor(width, height)
  return `${Math.round(width / divisor)}:${Math.round(height / divisor)}`
}

function formatFileSize(sizeBytes?: number): string {
  if (!sizeBytes || sizeBytes <= 0) return ''
  const megabytes = sizeBytes / (1024 * 1024)
  return megabytes >= 10
    ? `${megabytes.toFixed(1)} MB`
    : `${megabytes.toFixed(2)} MB`
}

function requestValue(
  requestData: Record<string, unknown> | null | undefined,
  key: string
): string {
  const value = requestData?.[key]
  if (typeof value === 'string') return value.trim()
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return ''
}

export function VideoDialog({
  videoUrl,
  fallbackUrl,
  taskId,
  requestData,
  open,
  onOpenChange,
}: VideoDialogProps) {
  const { t } = useTranslation()
  const [playableUrl, setPlayableUrl] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [hasError, setHasError] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')
  const [isDownloading, setIsDownloading] = useState(false)
  const [videoMetadata, setVideoMetadata] = useState<VideoMetadata | null>(null)
  const [loadedVideoInfo, setLoadedVideoInfo] = useState<LoadedVideoInfo>({})

  useEffect(() => {
    if (!open || !videoUrl) {
      return
    }

    let objectUrl: string | null = null
    let cancelled = false

    const load = async () => {
      setIsLoading(true)
      setHasError(false)
      setErrorMessage('')
      setPlayableUrl('')
      setVideoMetadata(null)
      setLoadedVideoInfo({})
      const candidates = [videoUrl, fallbackUrl].filter(
        (candidate, index, all): candidate is string =>
          Boolean(candidate?.trim()) && all.indexOf(candidate) === index
      )
      let lastError: unknown
      for (const candidate of candidates) {
        try {
          const resolved = await loadAuthenticatedMediaUrl(candidate)
          if (cancelled) return
          if (resolved.revoke) {
            objectUrl = resolved.url
          }
          setLoadedVideoInfo({
            contentType: resolved.contentType,
            sizeBytes: resolved.sizeBytes,
          })
          setPlayableUrl(resolved.url)
          return
        } catch (err) {
          lastError = err
        }
      }
      if (lastError) {
        const err = lastError
        if (!cancelled) {
          setPlayableUrl('')
          setHasError(true)
          setIsLoading(false)
          if (err instanceof MediaLoadError) {
            if (err.status === 410 || err.status === 404) {
              setErrorMessage(
                t('Video has expired or been removed from upstream storage')
              )
            } else {
              setErrorMessage(err.message)
            }
          } else {
            setErrorMessage(t('Failed to load video'))
          }
        }
      }
    }

    void load()

    return () => {
      cancelled = true
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl)
      }
    }
  }, [fallbackUrl, open, t, videoUrl])

  const handleOpenChange = (newOpen: boolean) => {
    if (newOpen) {
      setIsLoading(true)
      setHasError(false)
      setErrorMessage('')
      setPlayableUrl('')
      setVideoMetadata(null)
      setLoadedVideoInfo({})
    }
    onOpenChange(newOpen)
  }

  const handleDownload = async () => {
    if (!videoUrl || hasError || isDownloading) return
    setIsDownloading(true)
    try {
      await downloadMediaFile(videoUrl, 'generated-video.mp4')
    } finally {
      setIsDownloading(false)
    }
  }

  const specifications: Array<{ label: string; value: string }> = []
  const requestedResolution =
    requestValue(requestData, 'effective_resolution') ||
    requestValue(requestData, 'resolution')
  const resolution = videoMetadata
    ? `${videoMetadata.width}×${videoMetadata.height}`
    : requestedResolution
  if (resolution)
    specifications.push({ label: t('Resolution'), value: resolution })

  const aspectRatio = videoMetadata
    ? formatAspectRatio(videoMetadata.width, videoMetadata.height)
    : requestValue(requestData, 'aspect_ratio')
  if (aspectRatio)
    specifications.push({ label: t('Aspect ratio'), value: aspectRatio })

  const requestedDuration = Number(requestData?.duration)
  const duration = videoMetadata?.duration || requestedDuration
  if (Number.isFinite(duration) && duration > 0) {
    specifications.push({
      label: t('Duration (seconds)'),
      value: duration.toFixed(1),
    })
  }

  const mode = requestValue(requestData, 'mode')
  if (mode) specifications.push({ label: t('Mode'), value: mode.toUpperCase() })

  if (typeof requestData?.audio === 'boolean') {
    specifications.push({
      label: t('Audio'),
      value: requestData.audio ? t('Enabled') : t('Disabled'),
    })
  }
  if (typeof requestData?.has_video === 'boolean') {
    specifications.push({
      label: t('Video'),
      value: requestData.has_video ? t('Enabled') : t('Disabled'),
    })
  }

  const contentType = loadedVideoInfo.contentType?.split(';')[0]
  if (contentType) {
    specifications.push({
      label: t('Format'),
      value: contentType.replace(/^video\//, '').toUpperCase(),
    })
  }
  const fileSize = formatFileSize(loadedVideoInfo.sizeBytes)
  if (fileSize) specifications.push({ label: t('Size'), value: fileSize })

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className='flex max-h-[min(88vh,640px)] flex-col gap-3 overflow-hidden sm:max-w-lg'>
        <DialogHeader className='shrink-0 gap-1.5'>
          <DialogTitle>{t('Video Preview')}</DialogTitle>
          {taskId ? (
            <div className='flex items-center gap-1.5'>
              <DialogDescription className='min-w-0 flex-1 truncate font-mono text-xs'>
                {t('Task ID:')} {taskId}
              </DialogDescription>
              <CopyButton
                value={taskId}
                variant='ghost'
                size='icon'
                tooltip={t('Copy to clipboard')}
              />
            </div>
          ) : (
            <DialogDescription>
              {t('View the generated video')}
            </DialogDescription>
          )}
        </DialogHeader>

        <div className='min-h-0 flex-1 space-y-3 overflow-y-auto pr-0.5'>
          <div className='bg-muted/30 relative flex max-h-[min(32vh,260px)] min-h-[140px] items-center justify-center rounded-lg border p-2'>
            {isLoading && !hasError && (
              <Skeleton className='absolute inset-2 rounded-md' />
            )}

            {isLoading && !hasError && (
              <div className='absolute inset-0 z-10 flex flex-col items-center justify-center gap-2 px-4'>
                <Loader2 className='text-muted-foreground size-6 animate-spin' />
                <p className='text-muted-foreground text-sm'>
                  {t('Loading video...')}
                </p>
              </div>
            )}

            {playableUrl ? (
              <video
                key={playableUrl}
                src={playableUrl}
                controls
                className={`max-h-[min(32vh,240px)] max-w-full rounded-md ${
                  isLoading || hasError ? 'opacity-0' : 'opacity-100'
                }`}
                onLoadedData={() => {
                  setIsLoading(false)
                  setHasError(false)
                }}
                onLoadedMetadata={(event) => {
                  const video = event.currentTarget
                  setVideoMetadata({
                    width: video.videoWidth,
                    height: video.videoHeight,
                    duration: Number.isFinite(video.duration)
                      ? video.duration
                      : 0,
                  })
                }}
                onError={() => {
                  setIsLoading(false)
                  setHasError(true)
                  setErrorMessage(t('Failed to load video'))
                }}
              />
            ) : null}

            {hasError && (
              <div className='absolute inset-0 z-10 flex items-center justify-center px-4 text-center'>
                <p className='text-muted-foreground text-sm leading-relaxed'>
                  {errorMessage || t('Failed to load video')}
                </p>
              </div>
            )}
          </div>

          {specifications.length > 0 ? (
            <div className='flex flex-wrap gap-1.5'>
              {specifications.map((specification) => (
                <Badge
                  key={specification.label}
                  variant='secondary'
                  className='font-normal'
                >
                  <span className='text-muted-foreground mr-1'>
                    {specification.label}
                  </span>
                  {specification.value}
                </Badge>
              ))}
            </div>
          ) : null}

          <p className='text-muted-foreground text-center text-xs'>
            {t('Generated images and videos are only kept for 3 days.')}
          </p>

          <MediaDialogFooter
            mediaUrl={videoUrl}
            disabled={isLoading || hasError}
            isDownloading={isDownloading}
            onDownload={() => void handleDownload()}
          />

          <RequestDataPanel data={requestData} />
        </div>
      </DialogContent>
    </Dialog>
  )
}
