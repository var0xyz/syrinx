import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
const config = {
  kit: {
    adapter: adapter({
      fallback: 'index.html',
      ssr: false
    }),
    serviceWorker: {
      register: false
    },
    output: {
      bundleStrategy: 'single'
    }
  }
};

export default config;

