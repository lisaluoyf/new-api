/*
Copyright (C) 2025 QuantumNous

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

export const supportedLanguages = [
  'zh-CN',
  'zh-TW',
  'en',
  'ja',
  'pt',
  'de',
  'fr',
  'tr',
  'it',
  'pl',
  'id',
  'ko',
  'es',
  'ru',
  'vi',
];

export const languageOptions = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁體中文' },
  { value: 'en', label: 'English' },
  { value: 'ja', label: '日本語' },
  { value: 'pt', label: 'Português' },
  { value: 'de', label: 'Deutsch' },
  { value: 'fr', label: 'Français' },
  { value: 'tr', label: 'Türkçe' },
  { value: 'it', label: 'Italiano' },
  { value: 'pl', label: 'Polski' },
  { value: 'id', label: 'Bahasa' },
  { value: 'ko', label: '한국어' },
  { value: 'es', label: 'Español' },
  { value: 'ru', label: 'Русский' },
  { value: 'vi', label: 'Tiếng Việt' },
];

export const apimasterLocaleMap = {
  zh: 'zh-CN',
  'zh-tw': 'zh-TW',
  en: 'en',
  ja: 'ja',
  pt: 'pt',
  de: 'de',
  fr: 'fr',
  tr: 'tr',
  it: 'it',
  pl: 'pl',
  id: 'id',
  ko: 'ko',
  es: 'es',
  ru: 'ru',
  vi: 'vi',
};

export const normalizeLanguage = (language) => {
  if (!language) {
    return language;
  }

  const normalized = language.trim().replace(/_/g, '-');
  const lower = normalized.toLowerCase();
  if (!/^[a-z]{2,3}(?:-[a-z0-9]{2,8})*$/i.test(lower)) {
    return 'en';
  }

  if (apimasterLocaleMap[lower]) {
    return apimasterLocaleMap[lower];
  }

  if (
    lower === 'zh' ||
    lower === 'zh-cn' ||
    lower === 'zh-sg' ||
    lower.startsWith('zh-hans')
  ) {
    return 'zh-CN';
  }

  if (
    lower === 'zh-tw' ||
    lower === 'zh-hk' ||
    lower === 'zh-mo' ||
    lower.startsWith('zh-hant')
  ) {
    return 'zh-TW';
  }

  if (lower === 'en' || lower.startsWith('en-')) {
    return 'en';
  }

  if (lower === 'ja' || lower.startsWith('ja-')) {
    return 'ja';
  }

  for (const language of ['pt', 'de', 'fr', 'tr', 'it', 'pl']) {
    if (lower === language || lower.startsWith(`${language}-`)) {
      return language;
    }
  }

  if (lower === 'id' || lower.startsWith('id-') || lower === 'in' || lower.startsWith('in-')) {
    return 'id';
  }

  if (lower === 'ko' || lower.startsWith('ko-')) {
    return 'ko';
  }

  if (lower === 'es' || lower.startsWith('es-')) {
    return 'es';
  }

  if (lower === 'ru' || lower.startsWith('ru-')) {
    return 'ru';
  }

  if (lower === 'vi' || lower.startsWith('vi-')) {
    return 'vi';
  }

  const matchedLanguage = supportedLanguages.find(
    (supportedLanguage) => supportedLanguage.toLowerCase() === lower,
  );

  return matchedLanguage || 'en';
};
