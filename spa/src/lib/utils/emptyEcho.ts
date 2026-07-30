/** Echo with no commentary — feeds should show the original instead. */
export function isBlankEcho(reed: { content?: string; echoing?: string } | null | undefined): boolean {
  return !!(reed?.echoing && !(reed.content || '').trim());
}
