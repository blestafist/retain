import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';
import { VitePWA } from 'vite-plugin-pwa';

export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      injectRegister: 'auto',
      manifest: {
        id: '/',
        name: 'Retain — Vocabulary Learning',
        short_name: 'Retain',
        description: 'Learn vocabulary with spaced repetition.',
        start_url: '/',
        scope: '/',
        display: 'standalone',
        background_color: '#FCF9F4',
        theme_color: '#3A4527',
        icons: [{ src: '/pwa.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' }],
      },
      workbox: {
        cleanupOutdatedCaches: true,
        navigateFallback: '/index.html',
        globPatterns: ['**/*.css', '**/*.html', '**/*.js', '**/*.svg', '**/*.woff2'],
        runtimeCaching: [{
          urlPattern: ({ url, request }) => request.method === 'GET' && url.pathname === '/schema/retain-v1.json',
          handler: 'StaleWhileRevalidate',
          options: { cacheName: 'retain-public-api', expiration: { maxEntries: 2, maxAgeSeconds: 86_400 } },
        }],
      },
    }),
  ],
  build: {
    outDir: 'dist/generated',
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: false,
      },
    },
  },
  test: {
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    server: {
      deps: {
        inline: ['@material/material-color-utilities'],
      },
    },
  },
});
