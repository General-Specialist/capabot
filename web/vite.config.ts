import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  plugins: [tailwindcss(), react()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  server: {
    proxy: {
      '/api': process.env.API_URL || 'http://localhost:9090',
      '/v1': process.env.API_URL || 'http://localhost:9090',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
