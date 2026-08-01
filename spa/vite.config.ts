import { sveltekit } from '@sveltejs/kit/vite';
import { SvelteKitPWA } from '@vite-pwa/sveltekit';
import { defineConfig } from 'vite';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import devtoolsJson from 'vite-plugin-devtools-json';
import { licensePlugin } from './vite-plugin-license.js';

const spaRoot = path.dirname(fileURLToPath(import.meta.url));
// openpgp/lightweight only lists a `browser` export; Node's resolver (used
// when Kit loads server chunks during build) ignores that condition.
const openpgpLightweight = path.resolve(
  spaRoot,
  'node_modules/openpgp/dist/lightweight/openpgp.min.mjs'
);

export default defineConfig({
  plugins: [
    devtoolsJson(),
    licensePlugin(),
    sveltekit(),
    SvelteKitPWA({
      // Custom SW: PGP signing + app-shell precache (injectManifest).
      registerType: 'autoUpdate',
      injectRegister: false,
      includeAssets: ['icons/icon.svg', 'icons/android-chrome-192x192.png', 'icons/android-chrome-512x512.png'],
      strategies: 'injectManifest',
      srcDir: 'src',
      filename: 'service-worker.ts',
      injectManifest: {
        globPatterns: ['client/**/*.{js,css,ico,png,svg,webp,webmanifest,html}'],
        // adapter-static writes index.html after Kit's client emit; add it explicitly
        // so NavigationRoute(createHandlerBoundToURL('index.html')) works offline.
        // revision must change every build — null makes Workbox treat the URL as
        // immutable, leaving a stale shell that points at deleted chunk hashes.
        additionalManifestEntries: [{ url: 'index.html', revision: `${Date.now()}` }]
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
        enabled: false
      }
    })
  ],
  define: {
    global: 'globalThis',
    'process.env.NODE_ENV': JSON.stringify(process.env.NODE_ENV ?? 'production')
  },
  resolve: {
    alias: {
      'openpgp/lightweight': openpgpLightweight
    },
    conditions: ['browser', 'import', 'module', 'default']
  },
  ssr: {
    noExternal: ['openpgp'],
    resolve: {
      conditions: ['browser', 'import', 'module', 'default'],
      externalConditions: ['browser', 'import', 'module', 'default']
    }
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
