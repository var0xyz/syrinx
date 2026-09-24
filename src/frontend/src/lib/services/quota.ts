import { getStorageQuota } from './pwa';

/** Quota fraction at or above which this device must evict before
 * storing new content. */
export const QUOTA_THRESHOLD = 0.85;

export type QuotaStatus = {
  used: number;
  total: number;
  /** Fraction of quota in use, 0 when the browser reports no quota. */
  ratio: number;
  overThreshold: boolean;
};

/** Current storage pressure, or null when the browser exposes no
 * estimate. Callers treat null as "not over threshold" — refusing to
 * store because we can't measure is worse than overfilling. */
export async function getQuotaStatus(): Promise<QuotaStatus | null> {
  const estimate = await getStorageQuota();
  if (!estimate || estimate.total <= 0) return null;

  const ratio = estimate.used / estimate.total;
  return {
    used: estimate.used,
    total: estimate.total,
    ratio,
    overThreshold: ratio >= QUOTA_THRESHOLD,
  };
}

/** Whether this device is at or above the eviction threshold. */
export async function isOverThreshold(): Promise<boolean> {
  const status = await getQuotaStatus();
  return status?.overThreshold ?? false;
}
