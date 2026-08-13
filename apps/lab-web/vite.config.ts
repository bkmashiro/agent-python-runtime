import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const acceptancePath = fileURLToPath(new URL('../../integration/e2e/spark_acceptance_test.go', import.meta.url));

export default defineConfig({
  plugins: [
    react(),
    {
      name: 'pysolate-demo-evidence',
      resolveId(id) { return id === 'virtual:pysolate-demo' ? '\0virtual:pysolate-demo' : undefined; },
      async load(id) {
        if (id !== '\0virtual:pysolate-demo') return undefined;
        const acceptanceSource = await readFile(acceptancePath, 'utf8');
        const allStart = acceptanceSource.indexOf('func runScenarioAllExecution');
        const allEnd = acceptanceSource.indexOf('\nfunc ', allStart + 5);
        const allSource = allStart >= 0 ? acceptanceSource.slice(allStart, allEnd >= 0 ? allEnd : undefined) : '';
        return `export const acceptanceSource=${JSON.stringify(allSource)};`;
      },
    },
  ],
  build: {
    target: 'es2022',
    sourcemap: true,
    rolldownOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('@codemirror') || id.includes('@uiw')) return 'editor';
          if (id.includes('@mantine') || id.includes('lucide-react')) return 'ui';
          if (id.includes('react-complex-tree') || id.includes('react-resizable-panels') || id.includes('@tanstack')) return 'debugger';
          if (id.includes('node_modules/react')) return 'react';
        },
      },
    },
  },
  server: { host: '127.0.0.1', strictPort: true },
  preview: { host: '127.0.0.1', strictPort: true },
});
