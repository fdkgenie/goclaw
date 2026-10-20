import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./locales/en.json";
import vi from "./locales/vi.json";
import zh from "./locales/zh.json";

i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, vi: { translation: vi }, zh: { translation: zh } },
  lng: "en",
  fallbackLng: "en",
  interpolation: { escapeValue: false },
});

export default i18n;
export const t = (key: string, params?: Record<string, string | number>): string => {
  return i18n.t(key, params) as string;
};
