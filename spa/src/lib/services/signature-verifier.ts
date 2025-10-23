/**
 * Response Signature Verification
 *
 * This module provides client-side verification of Signature headers
 * that are added to API responses by the responseSigner middleware.
 */

// Type definitions for OpenPGP.js
declare global {
  interface Window {
    openpgp?: {
      readKey: (options: { armoredKey: string }) => Promise<any>;
      readCleartextMessage: (options: { cleartextMessage: string }) => Promise<any>;
      readMessage: (options: { armoredMessage: string }) => Promise<any>;
      createMessage: (options: { text: string }) => Promise<any>;
      verify: (options: { message: any; signature?: any; verificationKeys: any }) => Promise<any>;
    };
  }
}

// Extend Response interface to include signatureValid property
declare global {
  interface Response {
    signatureValid?: boolean;
  }
}

/**
 * Builds a canonical header string from response headers
 * Headers are sorted alphabetically for consistency with server implementation
 *
 * @param headers - Response headers (Headers object or plain object)
 * @returns Canonical header string
 */
export function buildCanonicalHeaderString(headers: Headers | Record<string, string | string[]>): string {
  const headerMap: Record<string, string[]> = {};

  // Convert Headers object to plain object if needed
  if (headers instanceof Headers) {
    for (const [name, value] of headers.entries()) {
      // Skip the signature header itself
      if (name.toLowerCase() === 'signature') {
        continue;
      }

      if (!headerMap[name]) {
        headerMap[name] = [];
      }
      headerMap[name].push(value);
    }
  } else {
    // Plain object
    for (const [name, value] of Object.entries(headers)) {
      if (name.toLowerCase() === 'signature') {
        continue;
      }
      headerMap[name] = Array.isArray(value) ? value : [value];
    }
  }

  // Sort header names alphabetically
  const sortedNames = Object.keys(headerMap).sort();

  // Build canonical string
  const lines: string[] = [];
  for (const name of sortedNames) {
    // Sort values for this header
    const values = headerMap[name].sort();
    lines.push(`${name}: ${values.join(', ')}`);
  }

  return lines.join('\n');
}

/**
 * Adds PGP armor delimiters to a stripped signature
 *
 * @param signature - Signature without armor delimiters
 * @returns Signature with armor delimiters
 */
export function addArmorDelimiters(signature: string): string {
  return `-----BEGIN PGP SIGNATURE-----\n\n${signature}\n-----END PGP SIGNATURE-----`;
}

/**
 * Verifies the Signature header for a response
 * Now verifies the complete response (headers + body) instead of just headers
 *
 * @param response - Fetch API Response object
 * @param serverPublicKey - Armored PGP public key
 * @returns Promise that resolves to true if signature is valid
 */
export async function verifyResponseSignature(
  response: Response,
  serverPublicKey: string
): Promise<boolean> {
  try {
    // Get the signature header
    const escapedSignature = response.headers.get('Signature');
    if (!escapedSignature) {
      console.warn('No Signature header found');
      return false;
    }

    // Unescape newlines from the signature
    const signature = escapedSignature.replace(/\\n/g, '\n');

    // Get the response body
    const responseText = await response.text();

    // Build canonical header string
    const canonicalHeaders = buildCanonicalHeaderString(response.headers);

    // Create the complete response string (headers + body)
    const completeResponse = canonicalHeaders + '\n\n' + responseText;

    // Verify signature using OpenPGP.js (if available)
    if (typeof window !== 'undefined' && window.openpgp) {
      const publicKey = await window.openpgp.readKey({ armoredKey: serverPublicKey });

      // Add armor delimiters back to the signature
      const armoredSignature = addArmorDelimiters(signature);

      // Parse the detached signature
      const signatureMessage = await window.openpgp.readMessage({
        armoredMessage: armoredSignature
      });

      // Create a message from the complete response
      const message = await window.openpgp.createMessage({
        text: completeResponse
      });

      // Verify the detached signature
      const verificationResult = await window.openpgp.verify({
        message,
        signature: signatureMessage,
        verificationKeys: publicKey
      });

      const { verified } = verificationResult.signatures[0];
      await verified; // Throws if signature is invalid

      return true;
    } else {
      console.warn('OpenPGP.js not loaded, cannot verify signature');
      return false;
    }
  } catch (error) {
    console.error('Signature verification failed:', error);
    return false;
  }
}

/**
 * Fetch options interface
 */
export interface FetchOptions extends RequestInit {
  credentials?: RequestCredentials;
}

/**
 * Secure API request interface
 */
export interface SecureApiRequestOptions extends FetchOptions {
  serverPublicKey?: string;
}

/**
 * Fetch wrapper that automatically verifies response signatures
 *
 * @param url - URL to fetch
 * @param options - Fetch options
 * @param serverPublicKey - Server's public key for verification
 * @returns Promise that resolves to Response object with added 'signatureValid' property
 */
export async function secureApiRequest(
  url: string,
  options: SecureApiRequestOptions = {},
  serverPublicKey?: string
): Promise<Response> {
  const { serverPublicKey: keyFromOptions, ...fetchOptions } = options;
  const publicKey = serverPublicKey || keyFromOptions;

  const response = await fetch(url, {
    ...fetchOptions,
    credentials: 'include' // Include cookies for authentication
  });

  // Verify signature if public key provided
  if (publicKey) {
    response.signatureValid = await verifyResponseSignature(response, publicKey);

    if (!response.signatureValid) {
      console.warn(`Response signature invalid for ${url}`);
    }
  }

  return response;
}

/**
 * Example usage with the Syrinx API
 */
export async function exampleUsage(): Promise<void> {
  try {
    // 1. First, get the server's public key (you would fetch this from the API)
    const serverKeyResponse = await fetch('/api/users/me/server-key', {
      credentials: 'include'
    });
    const { publicKey } = await serverKeyResponse.json();

    // 2. Make a secure API request with signature verification
    const response = await secureApiRequest(
      '/api/users/me',
      { method: 'GET' },
      publicKey
    );

    // 3. Check if signature was valid
    if (response.signatureValid) {
      console.log('✓ Response signature verified successfully');
      const data = await response.json();
      console.log('User data:', data);
    } else {
      console.error('✗ Response signature verification failed');
      // Handle untrusted response
    }

  } catch (error) {
    console.error('API request failed:', error);
  }
}

/**
 * Manual verification example
 */
export async function manualVerificationExample(): Promise<void> {
  // Make request
  const response = await fetch('/api/users/me', {
    credentials: 'include'
  });

  // Extract signature
  const escapedSignature = response.headers.get('Signature');
  console.log('Escaped signature:', escapedSignature);

  // Unescape newlines from the signature
  const signature = escapedSignature.replace(/\\n/g, '\n');
  console.log('Unescaped signature:', signature);

  // Build canonical header string manually
  const headers: Record<string, string> = {};
  for (const [name, value] of response.headers.entries()) {
    if (name !== 'signature') {
      headers[name] = value;
    }
  }

  const canonical = buildCanonicalHeaderString(headers);
  console.log('Canonical headers:\n', canonical);

  // Add armor delimiters back to the signature for verification
  const armoredSignature = addArmorDelimiters(signature);
  console.log('Armored signature:\n', armoredSignature);

  // You would then verify this against the signature using OpenPGP
}

/**
 * Type guard to check if OpenPGP is available
 */
export function isOpenPGPAvailable(): boolean {
  return typeof window !== 'undefined' && typeof window.openpgp !== 'undefined';
}

/**
 * Get server public key from API
 */
export async function getServerPublicKey(): Promise<string | null> {
  try {
    const response = await fetch('/api/server/public-key', {
      credentials: 'include'
    });

    if (!response.ok) {
      throw new Error(`Failed to fetch server public key: ${response.status}`);
    }

    const data = await response.json();
    return data.publicKey || null;
  } catch (error) {
    console.error('Failed to get server public key:', error);
    return null;
  }
}

/**
 * Enhanced secure API request with automatic server key fetching
 */
export async function secureApiRequestWithAutoKey(
  url: string,
  options: FetchOptions = {}
): Promise<Response> {
  const serverPublicKey = await getServerPublicKey();
  return secureApiRequest(url, options, serverPublicKey);
}
