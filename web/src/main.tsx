import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/index.css'
import './i18n'
import { App } from './App'
import { applyTheme } from './lib/theme'

applyTheme()

// The mock API runs only in `--mode mock` builds; MODE is a build-time constant, so production drops this branch.
if (import.meta.env.MODE === 'mock') {
  const { startMocks } = await import('./mocks/browser')
  await startMocks()
}

const root = document.getElementById('root')
if (root) {
  createRoot(root).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}
