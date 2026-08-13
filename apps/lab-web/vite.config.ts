import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const sourcePath = fileURLToPath(new URL('../../examples/controller-boundaries/04-workflow-with-workspace.py', import.meta.url));
const catalogPath = fileURLToPath(new URL('../../examples/controller-boundaries/fixtures/catalog.json', import.meta.url));
const benchmarkPath = fileURLToPath(new URL('../../examples/controller-boundaries/fixtures/benchmark.json', import.meta.url));
const acceptancePath = fileURLToPath(new URL('../../integration/e2e/spark_acceptance_test.go', import.meta.url));

export default defineConfig({
  plugins: [
    react(),
    {
      name: 'pysolate-demo-evidence',
      resolveId(id) { return id === 'virtual:pysolate-demo' ? '\0virtual:pysolate-demo' : undefined; },
      async load(id) {
        if (id !== '\0virtual:pysolate-demo') return undefined;
        const [source, catalog, benchmark, acceptanceSource] = await Promise.all([
          readFile(sourcePath, 'utf8'), readFile(catalogPath, 'utf8'), readFile(benchmarkPath, 'utf8'), readFile(acceptancePath, 'utf8'),
        ]);
        const allStart = acceptanceSource.indexOf('func runScenarioAllExecution');
        const allEnd = acceptanceSource.indexOf('\nfunc ', allStart + 5);
        const allSource = allStart >= 0 ? acceptanceSource.slice(allStart, allEnd >= 0 ? allEnd : undefined) : '';
        return `export const pythonSource=${JSON.stringify(source)};export const acceptanceSource=${JSON.stringify(allSource)};export const catalog=${catalog};export const benchmark=${benchmark};`;
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
