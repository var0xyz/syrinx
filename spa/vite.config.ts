import { sveltekit } from '@sveltejs/kit/vite';
import { SvelteKitPWA } from '@vite-pwa/sveltekit';
import { defineConfig, type Plugin } from 'vite';
import path from 'node:path';
import { mkdirSync, writeFileSync } from 'node:fs';
import { execSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import devtoolsJson from 'vite-plugin-devtools-json';
import { licensePlugin } from './vite-plugin-license.js';

const spaRoot = path.dirname(fileURLToPath(import.meta.url));

// deploy/scripts/syrinx/update.sh sets GIT_COMMIT from the shallow clone it
// just built from (see BUILD_DIR/src). Falls back to `git rev-parse` for
// local dev builds run straight from a checkout.
function resolveAppVersion(): string {
  if (process.env.GIT_COMMIT) return process.env.GIT_COMMIT.trim();
  try {
    return execSync('git rev-parse HEAD', { cwd: spaRoot }).toString().trim();
  } catch {
    return 'unknown';
  }
}
// openpgp/lightweight only lists a `browser` export; Node's resolver (used
// when Kit loads server chunks during build) ignores that condition.
const openpgpLightweight = path.resolve(
  spaRoot,
  'node_modules/openpgp/dist/lightweight/openpgp.min.mjs'
);

/**
 * adapter-static SPA builds leave prerendered/ empty when Workbox runs.
 * @vite-pwa/sveltekit always appends a prerendered HTML glob unless another
 * prerendered/ pattern is present — either way Workbox warns on zero matches.
 * Drop a tiny placeholder before injectManifest so the glob is satisfied, then
 * strip it from the precache manifest.
 */
function pwaPrerenderedPlaceholder(): Plugin {
  const keepDir = path.join(spaRoot, '.svelte-kit/output/prerendered');
  // Must not be dotfile — workbox/glob ignores dotfiles by default.
  const keepFile = path.join(keepDir, 'pwa-keep.html');
  return {
    name: 'syrinx-pwa-prerendered-placeholder',
    apply: 'build',
    buildStart() {
      mkdirSync(keepDir, { recursive: true });
      writeFileSync(keepFile, '<!-- pwa glob placeholder -->\n');
    },
  };
}

export default defineConfig({
  plugins: [
    devtoolsJson(),
    licensePlugin(),
    sveltekit(),
    pwaPrerenderedPlaceholder(),
    SvelteKitPWA({
      // Custom SW: PGP signing + app-shell precache (injectManifest).
      registerType: 'autoUpdate',
      injectRegister: false,
      includeAssets: ['icons/icon.svg', 'icons/android-chrome-192x192.png', 'icons/android-chrome-512x512.png'],
      strategies: 'injectManifest',
      srcDir: 'src',
      filename: 'service-worker.ts',
      injectManifest: {
        globPatterns: [
          'client/**/*.{js,css,ico,png,svg,webp,webmanifest,html}',
          'prerendered/**/*.html',
        ],
        // adapter-static writes index.html after Kit's client emit; add it explicitly
        // so NavigationRoute(createHandlerBoundToURL('index.html')) works offline.
        // revision must change every build — null makes Workbox treat the URL as
        // immutable, leaving a stale shell that points at deleted chunk hashes.
        additionalManifestEntries: [{ url: 'index.html', revision: `${Date.now()}` }],
        manifestTransforms: [
          async (entries) => ({
            manifest: entries.filter((e) => e.url !== 'prerendered/pwa-keep.html'),
            warnings: [],
          }),
        ],
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
    __APP_VERSION__: JSON.stringify(resolveAppVersion()),
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
