import { describe, it, expect } from "vitest";
import pkg from "../package.json";

const flatConfigFiles = (
  import.meta as unknown as { glob: (pattern: string) => Record<string, unknown> }
).glob("../eslint.config.js");

// The CI dashboard job runs `pnpm lint`. The lint script used to call eslint
// while the package declared no eslint dependency at all, so the job failed
// with "eslint: command not found" and the flat config was missing.
describe("dashboard lint tooling", () => {
  it("declares the eslint dependency the lint script runs", () => {
    expect(pkg.devDependencies.eslint).toBeDefined();
    expect(pkg.devDependencies["typescript-eslint"]).toBeDefined();
  });

  it("runs eslint from the lint script with flat-config options", () => {
    expect(pkg.scripts.lint).toContain("eslint");
    // --ext was removed with the legacy eslintrc config; the flat config
    // selects the linted files itself.
    expect(pkg.scripts.lint).not.toContain("--ext");
  });

  it("ships a flat eslint config", () => {
    expect(Object.keys(flatConfigFiles)).toContain("../eslint.config.js");
  });
});
