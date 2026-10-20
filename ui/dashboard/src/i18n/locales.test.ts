import { describe, it, expect } from "vitest";

import en from "./locales/en.json";
import vi from "./locales/vi.json";
import zh from "./locales/zh.json";

type Catalog = Record<string, string>;

const enCatalog = en as Catalog;
const viCatalog = vi as Catalog;
const zhCatalog = zh as Catalog;

const sortedKeys = (catalog: Catalog): string[] => Object.keys(catalog).sort();

describe("dashboard i18n catalogs", () => {
  it("keeps the same key set in en, vi and zh", () => {
    expect(sortedKeys(viCatalog)).toEqual(sortedKeys(enCatalog));
    expect(sortedKeys(zhCatalog)).toEqual(sortedKeys(enCatalog));
  });

  it("has a non-empty translation for every key", () => {
    for (const key of Object.keys(enCatalog)) {
      expect(enCatalog[key]?.trim(), key).not.toBe("");
      expect(viCatalog[key]?.trim(), key).not.toBe("");
      expect(zhCatalog[key]?.trim(), key).not.toBe("");
    }
  });

  it("keeps the same interpolation placeholders across locales", () => {
    const placeholders = (value: string): string[] =>
      Array.from(value.matchAll(/\{\{(\w+)\}\}/g))
        .map((m) => m[1])
        .sort();

    for (const key of Object.keys(enCatalog)) {
      expect(placeholders(viCatalog[key]), `${key} (vi)`).toEqual(
        placeholders(enCatalog[key]),
      );
      expect(placeholders(zhCatalog[key]), `${key} (zh)`).toEqual(
        placeholders(enCatalog[key]),
      );
    }
  });
});
