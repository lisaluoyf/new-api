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
import { ImageOff, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
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
import { isValidMediaPreviewURL } from '../../lib/media-preview'
import type { ImageAvailability } from '../../types'
import { ImageThumbnail } from '../image-thumbnail'
import { MediaDialogFooter } from './media-dialog-footer'
import { RequestDataPanel } from './request-data-panel'

interface ImageDialogProps {
  imageUrl: string
  imageUrls?: string[]
  taskId?: string
  errorMessage?: string
  errorCode?: string
  requestData?: Record<string, unknown> | null
  requestId?: string
  availabilityByUrl?: Record<string, ImageAvailability>
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function ImageDialog({
  imageUrl: initialImageUrl,
  imageUrls,
  taskId,
  errorMessage,
  errorCode,
  requestData,
  requestId,
  availabilityByUrl,
  open,
  onOpenChange,
}: ImageDialogProps) {
  const { t } = useTranslation()
  const [selectedUrl, setSelectedUrl] = useState('')
  const urls = imageUrls?.length ? imageUrls : [initialImageUrl]
  const imageUrl = urls.includes(selectedUrl) ? selectedUrl : initialImageUrl
  const hasValidUrl = isValidMediaPreviewURL(imageUrl)
  const availability = availabilityByUrl?.[imageUrl]
  const expired = availability?.status === 'expired'
  const [isLoading, setIsLoading] = useState(
    hasValidUrl && !expired && availability?.status !== 'unavailable'
  )
  const [hasError, setHasError] = useState(
    !hasValidUrl || availability?.status === 'unavailable'
  )
  const [isDownloading, setIsDownloading] = useState(false)
  const [retryCount, setRetryCount] = useState(0)
  const [retryUrl, setRetryUrl] = useState('')

  useEffect(() => {
    if (!open) return
    setIsLoading(
      hasValidUrl && !expired && availability?.status !== 'unavailable'
    )
    setHasError(!hasValidUrl || availability?.status === 'unavailable')
    setRetryCount(0)
    setRetryUrl('')
  }, [open, imageUrl, hasValidUrl, expired, availability?.status])

  const handleOpenChange = (newOpen: boolean) => {
    if (!newOpen) setSelectedUrl('')
    onOpenChange(newOpen)
  }

  const handleImageLoad = () => {
    setIsLoading(false)
    setHasError(false)
  }

  const handleImageError = () => {
    setIsLoading(false)
    setHasError(true)
  }

  const handleDownload = async () => {
    if (!hasValidUrl || expired || hasError || isDownloading) return
    setIsDownloading(true)
    try {
      await downloadMediaFile(imageUrl, 'generated-image.png', {
        allowLinkFallback: !availability,
      })
    } catch {
      setHasError(true)
      setIsLoading(false)
    } finally {
      setIsDownloading(false)
    }
  }

  const failureText =
    errorMessage ||
    (hasError && !hasValidUrl
      ? t('Image generation failed')
      : t('Failed to load image'))

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className='flex max-h-[min(88vh,640px)] flex-col gap-3 overflow-hidden sm:max-w-lg'>
        <DialogHeader className='shrink-0 gap-1.5'>
          <DialogTitle>{t('Image Preview')}</DialogTitle>
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
              {t('View the generated image')}
            </DialogDescription>
          )}
          {requestId ? (
            <div className='flex items-center gap-1.5'>
              <p className='text-muted-foreground min-w-0 flex-1 truncate font-mono text-xs'>
                {t('Request ID')}: {requestId}
              </p>
              <CopyButton
                value={requestId}
                variant='ghost'
                size='icon'
                tooltip={t('Copy to clipboard')}
              />
            </div>
          ) : null}
        </DialogHeader>

        <div className='min-h-0 flex-1 space-y-3 overflow-y-auto pr-0.5'>
          <div className='bg-muted/30 relative flex max-h-[min(32vh,260px)] min-h-[140px] items-center justify-center rounded-lg border p-2'>
            {expired ? (
              <div className='space-y-2 px-4 py-6 text-center'>
                <ImageOff className='text-muted-foreground mx-auto size-8' />
                <p className='text-sm font-medium'>{t('Image expired')}</p>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'The image has been removed after the retention period. The generation record is still available.'
                  )}
                </p>
              </div>
            ) : hasValidUrl ? (
              <>
                {isLoading && !hasError && (
                  <Skeleton className='absolute inset-2 rounded-md' />
                )}

                {isLoading && !hasError && (
                  <div className='absolute inset-0 z-10 flex flex-col items-center justify-center gap-2 px-4'>
                    <Loader2 className='text-muted-foreground size-6 animate-spin' />
                    <p className='text-muted-foreground text-sm'>
                      {t('Loading image...')}
                    </p>
                  </div>
                )}

                {!hasError &&
                  (availability?.status !== 'unavailable' ||
                    retryUrl === imageUrl) && (
                    <img
                      key={`${imageUrl}-${retryCount}`}
                      src={imageUrl}
                      alt={t('Generated image')}
                      className={`max-h-[min(32vh,240px)] max-w-full rounded-md object-contain ${
                        isLoading || hasError ? 'opacity-0' : 'opacity-100'
                      }`}
                      onLoad={handleImageLoad}
                      onError={handleImageError}
                    />
                  )}

                {hasError && (
                  <div className='absolute inset-0 z-10 flex flex-col items-center justify-center gap-2 px-4 text-center'>
                    <ImageOff className='text-muted-foreground size-6' />
                    <p className='text-sm font-medium'>
                      {t('Image unavailable')}
                    </p>
                    <p className='text-muted-foreground text-sm'>
                      {t('Unable to load this image. Please try again.')}
                    </p>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={() => {
                        setHasError(false)
                        setIsLoading(true)
                        setRetryCount((value) => value + 1)
                        setRetryUrl(imageUrl)
                      }}
                    >
                      {t('Retry')}
                    </Button>
                  </div>
                )}
              </>
            ) : (
              <div className='space-y-2 px-4 text-center'>
                <p className='text-destructive text-sm leading-relaxed break-words'>
                  {failureText}
                </p>
                {errorCode ? (
                  <p className='text-muted-foreground font-mono text-xs'>
                    {errorCode}
                  </p>
                ) : null}
              </div>
            )}
          </div>

          {hasValidUrl ? (
            <>
              {urls.length > 1 && (
                <div className='flex flex-wrap justify-center gap-2'>
                  {urls.map((url, index) => (
                    <button
                      key={url}
                      type='button'
                      aria-label={`${t('Generated image')} ${index + 1}`}
                      aria-pressed={url === imageUrl}
                      title={`${t('Generated image')} ${index + 1}`}
                      className={`size-14 shrink-0 overflow-hidden rounded-md border-2 focus-visible:outline-2 focus-visible:outline-offset-2 ${url === imageUrl ? 'border-primary' : 'border-transparent'}`}
                      onClick={() => setSelectedUrl(url)}
                    >
                      <ImageThumbnail
                        url={url}
                        availability={availabilityByUrl?.[url]}
                        className='size-full'
                      />
                    </button>
                  ))}
                </div>
              )}
              {availability ? (
                <p className='text-muted-foreground text-center text-xs'>
                  {t('Cached images are kept for {{hours}} hours.', {
                    hours: availability.retention_hours,
                  })}
                  {!expired && availability.expires_at ? (
                    <>
                      {' '}
                      {t('Download before {{time}}.', {
                        time: new Date(
                          availability.expires_at * 1000
                        ).toLocaleString(),
                      })}
                    </>
                  ) : null}
                </p>
              ) : (
                <p className='text-muted-foreground text-center text-xs'>
                  {t(
                    'Image availability depends on its storage provider. Download it promptly.'
                  )}
                </p>
              )}

              {!expired && !hasError && (
                <MediaDialogFooter
                  mediaUrl={imageUrl}
                  disabled={isLoading || hasError}
                  isDownloading={isDownloading}
                  onDownload={() => void handleDownload()}
                />
              )}
            </>
          ) : null}

          <RequestDataPanel data={requestData} />
        </div>
      </DialogContent>
    </Dialog>
  )
}
