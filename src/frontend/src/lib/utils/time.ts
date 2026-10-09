/** A wire timestamp (unix seconds) as a Date; 0 means unset. */
export function fromUnix(seconds: number): Date | undefined {
  return seconds ? new Date(seconds * 1000) : undefined;
}

/** A Date as wire unix seconds. */
export function toUnix(date: Date): number {
  return Math.floor(date.getTime() / 1000);
}

/**
 * Format a timestamp as relative time ("just now", "3 days ago", "a year ago").
 * Takes a Date or an ISO date string; wire timestamps go through fromUnix.
 */
export function formatRelativeTime(timestamp: string | Date | undefined): string {
  if (!timestamp) return '';

  const date = new Date(timestamp);
  const diffSeconds = Math.floor((Date.now() - date.getTime()) / 1000);

  if (diffSeconds < 15)   return 'just now';
  if (diffSeconds < 60)   return `${diffSeconds} seconds ago`;

  const diffMinutes = Math.floor(diffSeconds / 60);
  if (diffMinutes < 60)   return `${diffMinutes} minute${diffMinutes === 1 ? '' : 's'} ago`;

  const diffHours = Math.floor(diffMinutes / 60);
  if (diffHours < 24)     return `${diffHours} hour${diffHours === 1 ? '' : 's'} ago`;

  const diffDays = Math.floor(diffHours / 24);
  if (diffDays < 30)      return `${diffDays} day${diffDays === 1 ? '' : 's'} ago`;

  const diffMonths = Math.floor(diffDays / 30);
  if (diffMonths < 12)    return `${diffMonths} month${diffMonths === 1 ? '' : 's'} ago`;

  const diffYears = Math.floor(diffMonths / 12);
  return diffYears === 1 ? 'a year ago' : `${diffYears} years ago`;
}

/**
 * Format timestamp as absolute date and time (MMM DD, YYYY at HH:MM, 24h)
 */
export function formatAbsoluteDateTime(timestamp: string | Date | undefined): string {
  if (!timestamp) return '';

  const date = new Date(timestamp);
  const dateStr = date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  });
  const timeStr = date.toLocaleTimeString('en-US', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  });
  return `${dateStr} at ${timeStr}`;
}
