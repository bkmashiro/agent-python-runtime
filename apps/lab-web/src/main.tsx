import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MantineProvider, createTheme } from '@mantine/core';
import '@mantine/core/styles.css';
import App from './App';

const theme = createTheme({
  primaryColor: 'violet',
  fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
  fontFamilyMonospace: '"SFMono-Regular", Consolas, "Liberation Mono", monospace',
  defaultRadius: 'sm',
});

createRoot(document.getElementById('root')!).render(
  <StrictMode><MantineProvider theme={theme} defaultColorScheme="dark"><App /></MantineProvider></StrictMode>,
);
