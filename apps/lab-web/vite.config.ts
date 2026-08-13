import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
export default defineConfig({
  plugins: [react()],
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
