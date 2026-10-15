/**
 * Lets node harnesses import modules that use SvelteKit's `$lib` alias:
 * `node --import ./scripts/lib-alias.mjs`.
 */
import { register } from 'node:module';

register(
  'data:text/javascript,' +
    encodeURIComponent(`
      const lib = ${JSON.stringify(new URL('../src/lib/', import.meta.url).href)};
      export async function resolve(specifier, context, next) {
        if (specifier.startsWith('$lib/')) {
          const path = specifier.slice('$lib/'.length);
          return next(lib + (/\\.[a-z]+$/.test(path) ? path : path + '.ts'), context);
        }
        return next(specifier, context);
      }
    `)
);
