import { configDefaults, defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { execSync } from "node:child_process";
import { readFileSync } from "node:fs";

export default defineConfig({
  plugins: [react()],
  server: {
    fs: {
      allow: [".."],
    },
  },
  define: {
    __APP_VERSION__: JSON.stringify(getAppVersion()),
    __BUILD_SHA__: JSON.stringify(getBuildSha()),
    __BUILD_TIME__: JSON.stringify(new Date().toISOString()),
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: [
      "**/*.{test,spec}.?(c|m)[jt]s?(x)",
      "../bridge/__tests__/**/*.{test,spec}.?(c|m)[jt]s?(x)",
    ],
    exclude: [...configDefaults.exclude, "e2e/**"],
  },
});

function getAppVersion(): string {
  try {
    const pkg = JSON.parse(
      readFileSync(new URL("./package.json", import.meta.url), "utf-8")
    ) as { version?: string };
    return pkg.version ?? "0.0.0";
  } catch {
    return "0.0.0";
  }
}

function getBuildSha(): string {
  const envSha =
    process.env.GITHUB_SHA ??
    process.env.VERCEL_GIT_COMMIT_SHA ??
    process.env.RAILWAY_GIT_COMMIT_SHA ??
    process.env.CF_PAGES_COMMIT_SHA ??
    process.env.RENDER_GIT_COMMIT ??
    process.env.GIT_COMMIT ??
    process.env.COMMIT_SHA;

  if (envSha) {
    return envSha.slice(0, 12);
  }

  try {
    return execSync("git rev-parse --short=12 HEAD", {
      stdio: ["ignore", "pipe", "ignore"],
    })
      .toString()
      .trim();
  } catch {
    return "dev";
  }
}
