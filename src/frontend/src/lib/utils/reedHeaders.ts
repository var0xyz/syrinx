/** The signed headers of a reed. */
export interface HeaderedReed {
  id: string;
  userID: string;
  replying?: { to: string; root: string } | null;
  echoing?: string | null;
  thread?: { head: string; index: number } | null;
}

/** Why a reed's headers contradict each other, or null: a thread part
 * never replies or echoes, and each header is complete. */
export function reedShapeProblem(reed: HeaderedReed): string | null {
  if (reed.replying && (!reed.replying.to || !reed.replying.root)) return 'reply without parent or root';
  if (reed.thread && reed.replying) return 'thread part that is also a reply';
  if (reed.thread && reed.echoing) return 'thread part that is also an echo';
  if (reed.thread && (!reed.thread.head || !Number.isInteger(reed.thread.index) || reed.thread.index < 0)) {
    return 'thread part without head or index';
  }
  return null;
}
