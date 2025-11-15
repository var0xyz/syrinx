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
