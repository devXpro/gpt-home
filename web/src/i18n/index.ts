import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import type { Lang } from '@/lib/types'
import en from './en.json'
import ru from './ru.json'

// Keys are flat ("home.attention" and "home.attention.more" both exist, DESIGN.md §8), so the key separator is off.
void i18n.use(initReactI18next).init({
  resources: { ru: { translation: ru }, en: { translation: en } },
  lng: 'ru',
  fallbackLng: 'ru',
  supportedLngs: ['ru', 'en'],
  keySeparator: false,
  nsSeparator: false,
  interpolation: { escapeValue: false },
  returnNull: false,
  initAsync: false,
})

export function currentLang(): Lang {
  return i18n.language === 'en' ? 'en' : 'ru'
}

/** setLanguage switches the UI at once, without a reload (DESIGN.md §11), and keeps <html lang> in step. */
export function setLanguage(lang: Lang) {
  if (i18n.language !== lang) void i18n.changeLanguage(lang)
  document.documentElement.lang = lang
}

export default i18n
