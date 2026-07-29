/**
 * Response Signature Verification
 *
 * This module provides client-side verification of Signature headers
 * that are added to API responses by the responseSigner middleware.
 */

import * as openpgp from 'openpgp/lightweight';

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

    const publicKey = await openpgp.readKey({ armoredKey: serverPublicKey });
    const signatureMessage = await openpgp.readMessage({
      armoredMessage: signature
    });
    const message = await openpgp.createMessage({
      text: completeResponse
    });
    const verificationResult = await openpgp.verify({
      message,
      signature: signatureMessage,
      verificationKeys: publicKey
    });

    const { verified } = verificationResult.signatures[0]!;
    await verified;

    return true;
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
 * Type guard to check if OpenPGP is available
 */
export function isOpenPGPAvailable(): boolean {
  return true;
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
