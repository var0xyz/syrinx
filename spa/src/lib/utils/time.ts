/**
 * Format a timestamp as relative time ("just now", "3 days ago", "a year ago").
 * Accepts an ISO date string or a numeric ms-since-epoch value.
 */
export function formatRelativeTime(timestamp: string | number): string {
  if (!timestamp) return '';

  const date = typeof timestamp === 'number' ? new Date(timestamp) : new Date(timestamp);
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
 * Format timestamp as absolute date and time (MMM DD, YYYY at HH:MM AM/PM)
 */
export function formatAbsoluteDateTime(timestamp: string): string {
  if (!timestamp) return '';

  const date = new Date(timestamp);
  const dateStr = date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  });
  const timeStr = date.toLocaleTimeString('en-US', {
    hour: 'numeric',
    minute: '2-digit',
    hour12: true
  });
  return `${dateStr} at ${timeStr}`;
}
