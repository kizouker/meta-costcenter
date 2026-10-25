import { defineConfig } from '@playwright/test';

// End-to-end checks of the dashboard in the installed Google Chrome; see tests/dashboard.spec.mjs.
export default defineConfig({
  testDir: 'tests',
  timeout: 120_000,
  reporter: 'list',
  use: {
    channel: process.env.CHROME_PATH ? undefined : 'chrome',
    launchOptions: process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {},
    headless: true,
  },
});
