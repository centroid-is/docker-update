import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  base: '/',
  build: {
    outDir: '../internal/api/dist',
    emptyOutDir: true,
  },
});
