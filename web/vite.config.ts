import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react-swc'
import { createRequire } from 'module'
import { resolve } from 'path'
import { defineConfig } from 'vitest/config'

const nodeModules = resolve(import.meta.dirname, 'node_modules')

// The About page used to hold its own `const VERSION`, and it had already
// drifted from the manifest ('0.1.0-beta' vs '0.1.0'). A version string is the
// build artifact's identity, so it comes from the thing that is built.
const { version } = createRequire(import.meta.url)('./package.json') as { version: string }

export default defineConfig({
  base: './',
  define: { __APP_VERSION__: JSON.stringify(version) },
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: [
      { find: '@', replacement: resolve(import.meta.dirname, 'src') },
      { find: '@wesui', replacement: resolve(import.meta.dirname, '../../wesui.git/src') },
      // Point at ESM entry files. Aliasing to the package directory makes Vite
      // bundle a namespace where create is not a function in the webview.
      { find: 'zustand/middleware', replacement: resolve(nodeModules, 'zustand/esm/middleware.mjs') },
      { find: 'zustand/vanilla', replacement: resolve(nodeModules, 'zustand/esm/vanilla.mjs') },
      { find: 'zustand/react', replacement: resolve(nodeModules, 'zustand/esm/react.mjs') },
      { find: 'zustand', replacement: resolve(nodeModules, 'zustand/esm/index.mjs') },
    ],
    dedupe: ['react', 'react-dom', 'zustand'],
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    manifest: true,
    rollupOptions: {
      input: resolve(import.meta.dirname, 'index.html'),
      output: {
        entryFileNames: 'assets/index.js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: (info) => info.name?.endsWith('.css') ? 'assets/index.css' : 'assets/[name]-[hash][extname]',
      },
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
