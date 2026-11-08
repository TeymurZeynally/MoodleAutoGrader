import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App as AntApp, ConfigProvider } from 'antd'
import enGB from 'antd/locale/en_GB'
import { App } from './App'
import './style.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConfigProvider locale={enGB} theme={{ token: { colorPrimary: '#1668dc', borderRadius: 8 } }}>
      <AntApp><App /></AntApp>
    </ConfigProvider>
  </StrictMode>,
)
