import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { resources } from "@/i18n/resources";
import {
  DEFAULT_LANGUAGE,
  detectBrowserLanguage,
  normalizeLanguage,
  SUPPORTED_LANGUAGES,
  type AppLanguage
} from "@/lib/language";

void i18n.use(initReactI18next).init({
  resources,
  lng: detectBrowserLanguage(),
  fallbackLng: DEFAULT_LANGUAGE,
  supportedLngs: [...SUPPORTED_LANGUAGES],
  defaultNS: "common",
  ns: ["common"],
  interpolation: {
    escapeValue: false
  }
});

export function getCurrentLanguage(): AppLanguage {
  return normalizeLanguage(i18n.resolvedLanguage ?? i18n.language);
}

export { i18n };
