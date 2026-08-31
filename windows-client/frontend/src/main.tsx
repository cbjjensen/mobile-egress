import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { productDisplayName } from './branding.js'
import './styles.css'

document.title = productDisplayName

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
