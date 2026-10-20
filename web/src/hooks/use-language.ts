import { useCallback, useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
  getHtmlLang,
  normalizeLanguage,
  setStoredLanguage,
  type AppLanguage
} from "@/lib/language";

/** Keep the document chrome (lang/title/description) in sync with the UI language. */
function applyDocumentLanguage(language: AppLanguage, t: (key: string) => string) {
  document.documentElement.setAttribute("data-language", language);
  document.documentElement.lang = getHtmlLang(language);
  document.title = t("app.documentTitle");

  const description = document.querySelector('meta[name="description"]');
  if (description) {
    description.setAttribute("content", t("app.description"));
  }
  const appleTitle = document.querySelector('meta[name="apple-mobile-web-app-title"]');
  if (appleTitle) {
    appleTitle.setAttribute("content", t("app.documentTitle"));
  }
}

export function useLanguage() {
  const { i18n, t } = useTranslation();

  const language = normalizeLanguage(i18n.resolvedLanguage ?? i18n.language);

  useEffect(() => {
    applyDocumentLanguage(language, t);
  }, [language, t]);

  const setLanguage = useCallback(
    async (nextLanguage: AppLanguage) => {
      const normalized = normalizeLanguage(nextLanguage);
      await setStoredLanguage(normalized);
      await i18n.changeLanguage(normalized);
    },
    [i18n]
  );

  return {
    language,
    setLanguage
  };
}
