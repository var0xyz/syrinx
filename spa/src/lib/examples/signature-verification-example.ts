/**
 * Example usage of the signature verification service
 *
 * This file demonstrates how to use the signature-verifier service
 * in a SvelteKit application.
 */

import {
  secureApiRequest,
  secureApiRequestWithAutoKey,
  verifyResponseSignature,
  buildCanonicalHeaderString,
  isOpenPGPAvailable
} from '$lib/services/signature-verifier';

/**
 * Example: Basic API request with signature verification
 */
export async function exampleBasicRequest() {
  try {
    // Make a secure API request
    const response = await secureApiRequest('/api/users/me', {
      method: 'GET'
    }, 'your-server-public-key-here');

    if (response.signatureValid) {
      console.log('✓ Response signature verified');
      const data = await response.json();
      return data;
    } else {
      console.error('✗ Response signature invalid');
      return null;
    }
  } catch (error) {
    console.error('Request failed:', error);
    return null;
  }
}

/**
 * Example: API request with automatic server key fetching
 */
export async function exampleAutoKeyRequest() {
  try {
    const response = await secureApiRequestWithAutoKey('/api/users/me', {
      method: 'GET'
    });

    if (response.signatureValid) {
      console.log('✓ Response signature verified');
      return await response.json();
    } else {
      console.error('✗ Response signature invalid');
      return null;
    }
  } catch (error) {
    console.error('Request failed:', error);
    return null;
  }
}

/**
 * Example: Manual signature verification
 */
export async function exampleManualVerification() {
  try {
    // Make a regular fetch request
    const response = await fetch('/api/users/me', {
      credentials: 'include'
    });

    // Get the server's public key
    const serverKeyResponse = await fetch('/api/server/public-key', {
      credentials: 'include'
    });
    const { publicKey } = await serverKeyResponse.json();

    // Manually verify the signature
    const isValid = await verifyResponseSignature(response, publicKey);

    if (isValid) {
      console.log('✓ Manual verification successful');
      return await response.json();
    } else {
      console.error('✗ Manual verification failed');
      return null;
    }
  } catch (error) {
    console.error('Manual verification failed:', error);
    return null;
  }
}

/**
 * Example: Debug header canonicalization
 */
export function exampleDebugHeaders(response: Response) {
  console.log('Response headers:');
  for (const [name, value] of response.headers.entries()) {
    console.log(`  ${name}: ${value}`);
  }

  const canonical = buildCanonicalHeaderString(response.headers);
  console.log('Canonical header string:');
  console.log(canonical);
}

/**
 * Example: Check if OpenPGP is available
 */
export function exampleCheckOpenPGP() {
  if (isOpenPGPAvailable()) {
    console.log('✓ OpenPGP.js is available');
    return true;
  } else {
    console.warn('✗ OpenPGP.js is not available');
    return false;
  }
}

/**
 * Example: SvelteKit load function with signature verification
 */
export async function exampleSvelteKitLoad() {
  // This would be used in a +page.ts or +layout.ts file
  try {
    const response = await secureApiRequestWithAutoKey('/api/users/me');

    if (response.signatureValid) {
      return {
        user: await response.json(),
        signatureValid: true
      };
    } else {
      return {
        user: null,
        signatureValid: false,
        error: 'Invalid response signature'
      };
    }
  } catch (error) {
    return {
      user: null,
      signatureValid: false,
      error: error instanceof Error ? error.message : 'Unknown error'
    };
  }
}
