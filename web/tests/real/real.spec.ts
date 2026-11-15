import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { expect, type Page, test } from '@playwright/test'
import { guardCSP } from '../helpers'

// One story against a real homed (PLAN T2.6), nothing mocked: the LAN first-run page, the owner's passkey from a
// setup link (real WebAuthn ceremonies with Chrome's virtual authenticator), the home, a plugin tool run from the
// admin with live progress, an access key, a REST façade call with it as a ChatGPT Site would make, the call in the
// activity log, and the revocation. global-setup.ts starts homed; run it with `make e2e`.

function env(name: string): string {
  const v = process.env[name]
  if (!v) throw new Error(`${name} is not set: run this suite with \`make e2e\``)
  return v
}

/** homed runs the CLI against the test's homed, over its socket. */
function homed(...args: string[]): string {
  return execFileSync(env('E2E_HOMED'), [...args, '--data-dir', env('E2E_DATA_DIR')], {
    encoding: 'utf8',
    env: { ...process.env, HOMED_DATA_DIR: env('E2E_DATA_DIR') },
  })
}

/** confirmStepUpIfAsked answers the passkey prompt for a dangerous action when the session's step-up is stale. */
async function confirmStepUpIfAsked(page: Page, done: ReturnType<Page['locator']>) {
  const stepUp = page.getByRole('dialog', { name: 'Подтвердите, что это вы' })
  await expect(done.or(stepUp).first()).toBeVisible()
  if (await stepUp.isVisible()) await stepUp.getByRole('button', { name: 'Подтвердить' }).click()
}

test('first run → passkey → home → tool with progress → key → REST façade → revoke', async ({ page, request }) => {
  const csp = await guardCSP(page)
  const origin = env('E2E_ORIGIN')
  const api = env('E2E_API')
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  })

  await test.step('the LAN page: the PIN homed printed, then the server code (no relay in development)', async () => {
    await page.goto(`${env('E2E_SETUP')}/firstrun`)
    await expect(page.getByRole('heading', { name: 'Введите PIN' })).toBeVisible()
    const pin = /enter the PIN (\d{6})/.exec(readFileSync(env('E2E_LOG'), 'utf8'))?.[1] ?? ''
    expect(pin).toMatch(/^\d{6}$/)
    await page.getByRole('textbox', { name: 'Введите PIN' }).fill(pin)
    await expect(page.getByRole('heading', { name: 'Код сервера' })).toBeVisible()
  })

  await test.step('`homed admin setup-link` → the wizard → a real passkey makes Roman the owner', async () => {
    const link = homed('admin', 'setup-link').trim()
    expect(link.startsWith(`${origin}/admin/setup#t=`)).toBe(true)
    await page.goto(link)
    await expect(page.getByRole('heading', { name: 'Как назовём дом?' })).toBeVisible()
    expect(page.url()).not.toContain('#t=')
    await page.getByRole('textbox', { name: 'Название дома' }).fill('Мой шалаш')
    await page.getByRole('button', { name: 'Дальше' }).click()
    await expect(page.getByRole('heading', { name: 'Как вас зовут?' })).toBeVisible()
    await page.getByRole('textbox', { name: 'Ваше имя' }).fill('Роман')
    await page.getByRole('button', { name: 'Дальше' }).click()
    await page.getByRole('button', { name: 'Создать ключ' }).click()
    await expect(page.getByRole('heading', { name: 'Готово! Дом ваш' })).toBeVisible()
    const { credentials } = await cdp.send('WebAuthn.getCredentials', { authenticatorId })
    expect(credentials.map((c) => c.rpId)).toEqual(['localhost'])
    await page.getByRole('button', { name: 'Открыть дом' }).click()
    await expect(page.getByRole('heading', { name: 'Мой шалаш', level: 1 })).toBeVisible()
    // The LAN page closes once the home has an owner with a passkey.
    expect((await request.get(`${env('E2E_SETUP')}/api/firstrun/state`)).status()).toBe(410)
  })

  await test.step('Устройства → Fake: a tool runs from the admin and shows its live progress', async () => {
    const nav = page.getByRole('navigation', { name: 'Разделы' })
    await nav.getByRole('link', { name: 'Устройства' }).click()
    await page.getByRole('link', { name: /Fake/ }).click()
    // Assistant tools run from "what it can do" just for testing (DESIGN.md §9.2).
    // Untitled plugin tools carry their name as the title.
    await page.getByRole('region', { name: 'Что умеет в ChatGPT' }).getByRole('button', { name: /^fake_progress/ }).click()
    await page.getByRole('dialog', { name: 'fake_progress' }).getByRole('button', { name: 'Проверить без ChatGPT' }).click()
    await expect(page.getByText(/Нажмите OK|Almost done/)).toBeVisible()
    // fake_progress waits for fake_release, the way a TV pairing waits for OK on the remote.
    expect(homed('tool', 'call', 'fake_release').trim()).toBe('ok')
    await expect(page.getByRole('heading', { name: 'Готово' })).toBeVisible()
    await expect(page.getByText('released')).toBeVisible()
    await page.getByRole('button', { name: 'Готово' }).click()
    await expect(page.getByText('released')).toBeHidden()
  })

  let secret = ''
  await test.step('Настройки → Ключи доступа: a new key, shown once', async () => {
    await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Настройки' }).click()
    await page.getByRole('link', { name: 'Ключи доступа' }).click()
    await page.getByRole('button', { name: 'Создать ключ' }).click()
    const sheet = page.getByRole('dialog', { name: 'Создать ключ' })
    await sheet.getByRole('textbox', { name: 'Для чего' }).fill('E2E Site')
    await sheet.getByRole('button', { name: 'Создать ключ' }).click()
    await confirmStepUpIfAsked(page, sheet.locator('code'))
    await expect(sheet.locator('code')).toContainText('ghk_')
    secret = ((await sheet.locator('code').textContent()) ?? '').trim()
    expect(secret).toMatch(/^ghk_[A-Za-z0-9_-]{43}$/)
    await sheet.getByRole('button', { name: 'Готово' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Да, сохранил' }).click()
    await expect(sheet).toBeHidden()
    await expect(page.getByRole('button', { name: /E2E Site/ })).toBeVisible()
  })

  await test.step('the REST façade takes the key, as a ChatGPT Site would', async () => {
    const auth = { Authorization: `Bearer ${secret}` }
    const list = await request.get(`${api}/api/v1/tools`, { headers: auth })
    expect(list.status()).toBe(200)
    const manifest = (await list.json()) as { instructions: string; tools: { name: string }[] }
    expect(manifest.instructions).toContain('"Мой шалаш"')
    const names = manifest.tools.map((t) => t.name)
    expect(names).toContain('fake_echo')
    expect(names).not.toContain('fake_add') // admin tools never reach assistants
    const call = await request.post(`${api}/api/v1/tools/fake_echo`, { headers: auth, data: { text: 'Привет из Site' } })
    expect(call.status()).toBe(200)
    expect(await call.json()).toMatchObject({ text: 'Привет из Site', caller: 'Роман', via: 'site' })
  })

  await test.step('the call is in the activity log', async () => {
    await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Журнал' }).click()
    await expect(page.getByRole('link').filter({ hasText: 'Эхо' }).filter({ hasText: 'Роман · через ChatGPT' }).first()).toBeVisible()
  })

  await test.step('revoking the key in the admin locks the REST façade out at once', async () => {
    // A direct load of the deep link: the session cookie carries over a reload.
    await page.goto(`${origin}/admin/settings/keys`)
    await page.getByRole('button', { name: /E2E Site/ }).click()
    const confirm = page.getByRole('alertdialog')
    await expect(confirm).toContainText('Отозвать ключ «E2E Site»?')
    await confirm.getByRole('button', { name: 'Отозвать' }).click()
    // The toast (a screen-reader live region repeats the text, hence the span).
    const revoked = page.locator('span').filter({ hasText: /^Ключ отозван$/ })
    await confirmStepUpIfAsked(page, revoked)
    await expect(revoked).toBeVisible()
    await expect(page.getByRole('button', { name: /E2E Site/ })).toBeHidden()
    const res = await request.get(`${api}/api/v1/tools`, { headers: { Authorization: `Bearer ${secret}` } })
    expect(res.status()).toBe(401)
  })

  await csp.expectClean()
})
