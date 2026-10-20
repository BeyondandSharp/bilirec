import { sharedStore } from "@/lib/shared-store";

export const LANGUAGE_STORAGE_KEY = "app-language";
export const SUPPORTED_LANGUAGES = ["zh-TW", "zh-CN", "en"] as const;
/** Fallback language when nothing better is detected (CONFIG-V2 §6). */
export const DEFAULT_LANGUAGE = "en";

export type AppLanguage = (typeof SUPPORTED_LANGUAGES)[number];

/** i18n key used to label each language in menus. */
export const LANGUAGE_LABEL_KEYS: Record<AppLanguage, string> = {
  "zh-TW": "language.traditionalChinese",
  "zh-CN": "language.simplifiedChinese",
  en: "language.english"
};

/** Returns null when the input does not name a supported language. */
function matchLanguage(input?: string | null): AppLanguage | null {
  if (!input) {
    return null;
  }
  const language = input.toLowerCase();
  if (
    language.startsWith("zh-cn") ||
    language.startsWith("zh-sg") ||
    language.includes("hans")
  ) {
    return "zh-CN";
  }
  if (language.startsWith("zh")) {
    return "zh-TW";
  }
  if (language.startsWith("en")) {
    return "en";
  }
  return null;
}

export function normalizeLanguage(input?: string | null): AppLanguage {
  return matchLanguage(input) ?? DEFAULT_LANGUAGE;
}

export function detectBrowserLanguage(): AppLanguage {
  if (typeof navigator === "undefined") {
    return DEFAULT_LANGUAGE;
  }

  const candidates = [navigator.language, ...(navigator.languages ?? [])];
  for (const candidate of candidates) {
    const matched = matchLanguage(candidate);
    if (matched) {
      return matched;
    }
  }

  return DEFAULT_LANGUAGE;
}

/** BCP-47 language subtag for `document.documentElement.lang`. */
export function getHtmlLang(language: AppLanguage): string {
  switch (language) {
    case "zh-TW":
      return "zh-Hant";
    case "zh-CN":
      return "zh-Hans";
    default:
      return "en";
  }
}

/** Locale passed to `toLocaleString` / `Intl` formatters. */
export function getDateLocale(language: AppLanguage): string {
  switch (language) {
    case "zh-TW":
      return "zh-TW";
    case "zh-CN":
      return "zh-CN";
    default:
      return "en-US";
  }
}

export async function getStoredLanguage(): Promise<AppLanguage | null> {
  const value = await sharedStore.get<string>(LANGUAGE_STORAGE_KEY);
  if (!value) {
    return null;
  }

  return normalizeLanguage(value);
}

export async function setStoredLanguage(language: AppLanguage): Promise<void> {
  await sharedStore.set<AppLanguage>(LANGUAGE_STORAGE_KEY, language);
}
