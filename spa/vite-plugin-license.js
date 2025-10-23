import { readFileSync } from 'fs';
import { resolve } from 'path';

/**
 * Vite plugin that prepends LICENSE content as a comment to generated files
 */
export function licensePlugin() {
  let licenseContent = '';

  return {
    name: 'license-plugin',
    buildStart() {
      try {
        // Read the LICENSE file from the project root
        const licensePath = resolve(process.cwd(), '../LICENSE');
        const rawLicense = readFileSync(licensePath, 'utf-8');

        // Convert to JavaScript comment format
        const jsCommentLines = rawLicense
          .split('\n')
          .map(line => line.trim() === '' ? ' *' : ` * ${line}`)
          .join('\n');

        licenseContent = `/*\n${jsCommentLines}\n */\n\n`;
      } catch (error) {
        console.warn('Warning: Could not read LICENSE file:', error.message);
        licenseContent = '';
      }
    },

    generateBundle(options, bundle) {
      // Process all JavaScript and CSS files in the bundle
      for (const fileName in bundle) {
        const chunk = bundle[fileName];

        if (chunk.type === 'chunk' && (fileName.endsWith('.js') || fileName.endsWith('.css'))) {
          // Prepend license comment to the code
          chunk.code = licenseContent + chunk.code;
        } else if (chunk.type === 'asset' && fileName.endsWith('.css')) {
          // Handle CSS assets
          chunk.source = licenseContent + chunk.source;
        }
      }
    },

    renderChunk(code, chunk) {
      // Also handle chunks during the render phase
      if (chunk.fileName.endsWith('.js')) {
        return licenseContent + code;
      } else if (chunk.fileName.endsWith('.css')) {
        // For CSS files, the license content is already in the correct format
        return licenseContent + code;
      }
      return code;
    },

    transform(code, id) {
      // Handle CSS files during transformation
      if (id.endsWith('.css')) {
        return licenseContent + code;
      }
      return null;
    }
  };
}
