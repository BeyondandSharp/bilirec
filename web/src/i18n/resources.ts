import { common as en } from "@/i18n/locales/en";
import { common as zhCN } from "@/i18n/locales/zh-CN";
import { common as zhTW } from "@/i18n/locales/zh-TW";

/**
 * i18n resources for every supported UI language. The bulk of the strings
 * live in `src/i18n/locales/*.ts`; keep the three trees structurally identical
 * (a parity check runs as part of the frontend verification).
 */
export const resources = {
  en: { common: en },
  "zh-TW": { common: zhTW },
  "zh-CN": { common: zhCN }
};
