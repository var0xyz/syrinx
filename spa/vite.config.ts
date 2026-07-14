import { sveltekit } from '@sveltejs/kit/vite';
import { SvelteKitPWA } from '@vite-pwa/sveltekit';
import { defineConfig } from 'vite';
import devtoolsJson from 'vite-plugin-devtools-json';
import { licensePlugin } from './vite-plugin-license.js';

export default defineConfig({
  plugins: [
    devtoolsJson(),
    licensePlugin(),
    sveltekit(),
    SvelteKitPWA({
      registerType: 'autoUpdate',
      includeAssets: ['icons/icon.svg', 'icons/android-chrome-192x192.png', 'icons/android-chrome-512x512.png'],
      strategies: 'injectManifest',
      srcDir: 'static',
      filename: 'sw.js',
      workbox: {
        globPatterns: [
          '**/*.{js,css,html,ico,png,svg,woff2}',
          '**/.svelte-kit/generated/client/**/*.js',
          '**/_app/**/*.{js,css}'
        ],
        cleanupOutdatedCaches: true,
        skipWaiting: true,
        clientsClaim: true,
        runtimeCaching: [
          {
            urlPattern: /^https:\/\/fonts\.googleapis\.com\/.*/i,
            handler: 'CacheFirst',
            options: {
              cacheName: 'google-fonts-cache',
              expiration: {
                maxEntries: 10,
                maxAgeSeconds: 60 * 60 * 24 * 365 // 1 year
              },
              cacheableResponse: {
                statuses: [0, 200]
              }
            }
          },
          {
            urlPattern: /^https:\/\/fonts\.gstatic\.com\/.*/i,
            handler: 'CacheFirst',
            options: {
              cacheName: 'gstatic-fonts-cache',
              expiration: {
                maxEntries: 10,
                maxAgeSeconds: 60 * 60 * 24 * 365 // 1 year
              },
              cacheableResponse: {
                statuses: [0, 200]
              }
            }
          },
          {
            urlPattern: /^\/api\/.*/i,
            handler: 'NetworkFirst',
            options: {
              cacheName: 'api-cache',
              expiration: {
                maxEntries: 100,
                maxAgeSeconds: 60 * 60 * 24 // 1 day
              },
              networkTimeoutSeconds: 10,
              cacheableResponse: {
                statuses: [0, 200]
              }
            }
          },
          {
            urlPattern: /\.(?:png|jpg|jpeg|svg|gif|webp)$/i,
            handler: 'CacheFirst',
            options: {
              cacheName: 'images-cache',
              expiration: {
                maxEntries: 100,
                maxAgeSeconds: 60 * 60 * 24 * 30 // 30 days
              }
            }
          },
          {
            urlPattern: /\.svelte-kit\/generated\/client\/.*\.js$/i,
            handler: 'CacheFirst',
            options: {
              cacheName: 'sveltekit-client-cache',
              expiration: {
                maxEntries: 50,
                maxAgeSeconds: 60 * 60 * 24 * 7 // 7 days
              }
            }
          }
        ],
        navigateFallback: '/',
        navigateFallbackDenylist: [/^\/api\//, /^\/_app\//]
      },
      manifest: {
        name: 'Syrinx',
        short_name: 'Syrinx',
        description: 'A censorship-resistant, decentralized platform for free expression',
        start_url: '/',
        display: 'standalone',
        background_color: '#0b0f14',
        theme_color: '#0b0f14',
        orientation: 'portrait-primary',
        scope: '/',
        lang: 'en-US',
        dir: 'ltr',
        categories: ['social', 'communication', 'security'],
        icons: [
          {
            src: '/icons/android-chrome-192x192.png',
            sizes: '192x192',
            type: 'image/png',
            purpose: 'any'
          },
          {
            src: '/icons/android-chrome-256x256.png',
            sizes: '256x256',
            type: 'image/png',
            purpose: 'any'
          },
          {
            src: '/icons/android-chrome-512x512.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'any maskable'
          },
          {
            src: '/icons/icon.svg',
            sizes: 'any',
            type: 'image/svg+xml',
            purpose: 'any'
          }
        ],
        shortcuts: [
          {
            name: 'Reeds',
            short_name: 'Reeds',
            description: 'Write and view reeds',
            url: '/reeds',
            icons: [{ src: '/icons/android-chrome-192x192.png', sizes: '192x192' }]
          },
          {
            name: 'Feed',
            short_name: 'Feed',
            description: 'View your feed',
            url: '/feeds',
            icons: [{ src: '/icons/android-chrome-192x192.png', sizes: '192x192' }]
          }
        ],
        protocol_handlers: [
          {
            protocol: 'web+syrinx',
            url: '/to-do?resource=%s'
          }
        ]
      },
      devOptions: {
        enabled: true,
        type: 'module'
      }
    })
  ],
  define: {
    global: 'globalThis',
  },
  optimizeDeps: {
    include: ['openpgp/lightweight']
  },
  server: {
    proxy: {
      '/api': {
        target: `http://${process.env.API_HOST ?? 'localhost:8080'}`,
        changeOrigin: true
      },
      '/ws': {
        target: `http://${process.env.API_HOST ?? 'localhost:8080'}`,
        changeOrigin: true,
        ws: true
      }
    }
  }
});

