/**
 * Shared utilities for request signing that work in both main app and service worker contexts
 */

/**
 * Builds a canonical request string for signing
 * Matches the server-side implementation exactly
 */
export function buildCanonicalRequestString(
  method: string,
  path: string,
  headers: Headers | Record<string, string | string[]>,
  body: string = ''
): string {
  const builder: string[] = [];

  // Add method and path
  builder.push(`${method} ${path}`);

  // Convert Headers to plain object if needed
  const headerMap: Record<string, string[]> = {};

  if (headers instanceof Headers) {
    for (const [name, value] of headers.entries()) {
      // Exclude Syrinx authentication headers from the canonical string
      if (!name.toLowerCase().startsWith('x-syrinx-')) {
        if (!headerMap[name]) {
          headerMap[name] = [];
        }
        headerMap[name].push(value);
      }
    }
  } else {
    // Plain object
    for (const [name, value] of Object.entries(headers)) {
      if (!name.toLowerCase().startsWith('x-syrinx-')) {
        headerMap[name] = Array.isArray(value) ? value : [value];
      }
    }
  }

  // Sort header names alphabetically
  const sortedNames = Object.keys(headerMap).sort();

  // Add headers
  for (const name of sortedNames) {
    const values = headerMap[name].sort();
    builder.push(`${name}: ${values.join(', ')}`);
  }

  // Add body if present
  if (body) {
    builder.push(''); // Empty line before body
    builder.push(body);
  }

  return builder.join('\n');
}

/**
 * Escapes signature for safe HTTP header transmission
 */
export function escapeSignature(signature: string): string {
  return signature.replace(/\n/g, '\\n');
}

/**
 * Unescapes signature from HTTP header
 */
export function unescapeSignature(escapedSignature: string): string {
  return escapedSignature.replace(/\\n/g, '\n');
}

/**
 * Converts Headers object to plain object
 */
export function getHeadersObject(headers: Headers): Record<string, string[]> {
  const result: Record<string, string[]> = {};
  for (const [name, value] of headers.entries()) {
    if (!result[name]) {
      result[name] = [];
    }
    result[name].push(value);
  }
  return result;
}

/**
 * Adds PGP armor delimiters to a stripped signature
 */
export function addArmorDelimiters(signature: string): string {
  return `-----BEGIN PGP SIGNATURE-----\n\n${signature}\n-----END PGP SIGNATURE-----`;
}

/**
 * Strips PGP armor delimiters from a signature
 */
export function stripArmorDelimiters(signature: string): string {
  const lines = signature.split('\n');
  const result: string[] = [];

  for (const line of lines) {
    // Skip armor delimiters and empty lines
    if (
      line.startsWith('-----BEGIN PGP SIGNATURE-----') ||
      line.startsWith('-----END PGP SIGNATURE-----') ||
      line === ''
    ) {
      continue;
    }
    result.push(line);
  }

  return result.join('\n');
}
