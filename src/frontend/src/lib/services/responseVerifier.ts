/**
 * Verifies the `Signature` header responseSigner middleware (middlewares.go)
 * puts on every response. X-Syrinx-Signed-Headers names exactly which
 * headers were covered — read that rather than guessing (see middlewares.go).
 */

import { cryptoService } from './crypto';
import { getTrustedServerKey } from './serverKeyTrust';

const SIGNED_HEADERS_HEADER = 'X-Syrinx-Signed-Headers';

// Go's signer sorts across separate Set/Add calls for the same header
// name, never within one already-joined value — matched here by using
// headers.get() verbatim, since Fetch's Headers API only ever exposes
// the fully-joined string and can't distinguish the two cases either.
function buildCanonicalHeaderString(headers: Headers, signedNames: string[]): string {
  const sortedNames = [...signedNames].sort();
  const lines: string[] = [];
  for (const name of sortedNames) {
    const value = headers.get(name);
    if (value === null) continue;
    lines.push(`${name}: ${value}`);
  }
  return lines.join('\n');
}

/** Verifies res against the trusted server key. Fails closed (false) if
 * there's no Signature header, no trusted key yet, or a mismatch. */
export async function verifyResponseEnvelope(res: Response): Promise<boolean> {
  const escapedSignature = res.headers.get('Signature');
  if (!escapedSignature) return false;

  const signedNamesHeader = res.headers.get(SIGNED_HEADERS_HEADER);
  if (!signedNamesHeader) return false;
  const signedNames = signedNamesHeader.split(',').map((s) => s.trim()).filter(Boolean);

  const trusted = getTrustedServerKey();
  if (!trusted) return false;

  const signature = escapedSignature.replace(/\\n/g, '\n');
  const bodyText = await res.clone().text();
  const canonicalHeaders = buildCanonicalHeaderString(res.headers, signedNames);
  const completeResponse = `${canonicalHeaders}\n\n${bodyText}`;

  return cryptoService.verifyStrippedSignature(completeResponse, signature, trusted.armor);
}
