/** What applying a revocation needs from a reed. */
export interface RevocableReed {
  id: string;
  userSignature?: { id?: string } | null;
  serverSignature?: { signedAt?: number } | null;
}

/** A key stays valid for content it signed strictly before its revocation.
 * Both instants are unix seconds. */
export function signedBeforeRevocation(at: number, revokedAt: number): boolean {
  return at < revokedAt;
}

/** Reeds the revoked key signed at or after its revocation. */
export function reedsSignedAfterRevocation<T extends RevocableReed>(
  reeds: T[],
  keyID: string,
  revokedAt: number
): T[] {
  return reeds.filter(
    (reed) =>
      reed.userSignature?.id === keyID &&
      !!reed.serverSignature?.signedAt &&
      !signedBeforeRevocation(reed.serverSignature.signedAt, revokedAt)
  );
}
