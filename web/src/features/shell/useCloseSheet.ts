import { useNavigate } from 'react-router'
import { canGoBack } from './navstack'

/**
 * useCloseSheet closes a sheet that has its own route the way the back gesture would: back to wherever the person
 * opened it from, or to the page under it when the sheet was opened directly (a reload, a shared link).
 */
export function useCloseSheet(parent: string) {
  const navigate = useNavigate()
  return () => {
    if (canGoBack()) navigate(-1)
    else navigate(parent, { replace: true, preventScrollReset: true })
  }
}
