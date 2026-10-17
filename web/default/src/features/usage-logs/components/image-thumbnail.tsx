import { useState } from 'react'
import { ImageOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { ImageAvailability } from '../types'

export function ImageThumbnail(props: {
  url: string
  availability?: ImageAvailability
  className?: string
  onError?: () => void
}) {
  const { t } = useTranslation()
  const [failedUrl, setFailedUrl] = useState('')
  const unavailable =
    props.availability?.status === 'expired' ||
    props.availability?.status === 'unavailable' ||
    failedUrl === props.url

  return (
    <span
      className={cn(
        'border-border bg-muted flex shrink-0 items-center justify-center overflow-hidden rounded-md border',
        props.className
      )}
    >
      {unavailable ? (
        <ImageOff className='text-muted-foreground size-4' aria-hidden='true' />
      ) : (
        <img
          src={props.url}
          alt={t('Generated image')}
          className='size-full object-cover'
          loading='lazy'
          onError={() => {
            setFailedUrl(props.url)
            props.onError?.()
          }}
        />
      )}
    </span>
  )
}
