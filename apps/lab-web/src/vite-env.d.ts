/// <reference types="vite/client" />

declare module 'virtual:pysolate-demo' {
  export const pythonSource: string;
  export const catalog: { items: Array<{ id: string; score: number; title: string }> };
  export const benchmark: Record<string, unknown>;
}
