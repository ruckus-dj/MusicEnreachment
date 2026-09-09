import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
  },
  build: {
    outDir: '../src/music_ingest/ui/dist',
    emptyOutDir: true,
  },
})
