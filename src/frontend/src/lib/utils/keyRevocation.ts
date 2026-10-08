/** What applying a revocation needs from a reed. */
export interface RevocableReed {
  id: string;
  userSignature?: { id?: string } | null;
  serverSignature?: { timestamp?: string } | null;
}

/** A key stays valid for content it signed strictly before its revocation. */
export function signedBeforeRevocation(atISO: string, revokedAtISO: string): boolean {
  return Date.parse(atISO) < Date.parse(revokedAtISO);
}

/** Reeds the revoked key signed at or after its revocation. */
export function reedsSignedAfterRevocation<T extends RevocableReed>(
  reeds: T[],
  keyID: string,
  revokedAtISO: string
): T[] {
  return reeds.filter(
    (reed) =>
      reed.userSignature?.id === keyID &&
      !!reed.serverSignature?.timestamp &&
      !signedBeforeRevocation(reed.serverSignature.timestamp, revokedAtISO)
  );
}
