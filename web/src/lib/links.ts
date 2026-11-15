/** httpsLink keeps a link from a plugin's manifest (its homepage) only when it is a plain https URL with a host and no
 * credentials; homed filters it the same way. Anything else (javascript:, http:, a relative path) is not shown. */
export function httpsLink(raw?: string): URL | undefined {
  if (!raw) return undefined
  try {
    const u = new URL(raw.trim())
    return u.protocol === 'https:' && u.host !== '' && !u.username && !u.password ? u : undefined
  } catch {
    return undefined
  }
}
