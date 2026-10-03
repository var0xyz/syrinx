import { stripMarkdown } from '$lib/repositories/reeds';
import { notificationStore } from '$lib/stores/notifications';

/** Opens the native share sheet, falling back to copying the reed URL. */
export async function shareReed(reedRef: string, username: string, content = ''): Promise<void> {
  const url = `${window.location.origin}/reed/${reedRef}`;

  if (navigator.share) {
    try {
      await navigator.share({ title: `${username}'s Reed`, text: stripMarkdown(content), url });
    } catch (error) {
      if ((error as Error)?.name !== 'AbortError') {
        console.error('Error sharing:', error);
        notificationStore.error('Failed to share reed');
      }
    }
    return;
  }

  try {
    await navigator.clipboard.writeText(url);
    notificationStore.success('Reed URL copied to clipboard');
  } catch (error) {
    console.error('Error copying to clipboard:', error);
    notificationStore.error('Failed to copy reed URL');
  }
}
