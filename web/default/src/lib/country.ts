export const COUNTRY_CODES = [
  'CN', 'TW', 'HK', 'MO', 'US', 'GB', 'JP', 'KR', 'SG', 'MY', 'ID', 'TH',
  'VN', 'PH', 'IN', 'AU', 'CA', 'DE', 'FR', 'RU', 'BR', 'MX', 'NL', 'SE',
  'CH', 'IT', 'ES', 'PL', 'TR', 'SA', 'AE', 'IL', 'NZ', 'NO', 'FI', 'DK',
  'PT', 'CZ', 'RO', 'HU', 'UA', 'PK', 'BD', 'NG', 'ZA', 'EG', 'KE', 'AR',
  'CO', 'CL',
] as const

/** Returns { code, name } for a 2-letter country code. */
export function parseCountry(
  code: string | undefined | null,
  locale = 'en'
): { code: string; name: string } | null {
  if (!code) return null
  const upper = code.toUpperCase()
  try {
    const names = new Intl.DisplayNames([locale], { type: 'region' })
    return { code: upper, name: names.of(upper) || upper }
  } catch {
    return { code: upper, name: upper }
  }
}
