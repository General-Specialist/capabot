import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App'
import { AlertProvider } from '@/components/AlertProvider'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AlertProvider>
      <App />
    </AlertProvider>
  </StrictMode>
)
