import { defineConfig, devices } from '@playwright/test'

// Browser tests run against the mock build (`vite build --mode mock`) served by `vite preview` with the production
// CSP, so a style or script the policy blocks fails the run. The host is localhost: WebAuthn refuses IP addresses
// as relying party ids, and the smoke tests use Chrome's virtual authenticator.
const port = 4174

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  timeout: 45_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: `http://localhost:${port}`,
    trace: 'retain-on-failure',
    locale: 'ru-RU',
    timezoneId: 'Europe/Warsaw',
  },
  webServer: {
    command: `npm run build:mock && npx vite preview --mode mock --outDir .mock-dist --port ${port} --strictPort --host localhost`,
    url: `http://localhost:${port}/admin/`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [
    { name: 'smoke', testMatch: /smoke\.spec\.ts/, use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 860 } } },
    { name: 'screenshots', testMatch: /screenshots\.spec\.ts/, timeout: 600_000, use: { ...devices['Desktop Chrome'] } },
  ],
})
