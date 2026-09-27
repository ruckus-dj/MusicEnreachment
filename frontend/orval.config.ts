import { defineConfig } from 'orval'

export default defineConfig({
  api: {
    input: { target: './openapi.json' },
    output: {
      client: 'react-query',
      mode: 'split',
      mock: true,
      target: './src/api/generated/client.ts',
    },
  },
})
