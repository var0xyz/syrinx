/** What checking a thread bundle needs from a record and its parts. */
export interface BundleRecord {
  threadID: string;
  userID: string;
  reedIDs: string[];
}

export interface BundlePart {
  id: string;
  userID: string;
  thread?: { head: string; index: number } | null;
  replying?: string | null;
  echoing?: string | null;
}

/**
 * Why parts don't form the thread record describes, or null when they do:
 * one part per entry, in order, each by the author and naming its own place.
 */
export function threadBundleMismatch(
  threadID: string,
  record: BundleRecord,
  parts: BundlePart[]
): string | null {
  if (record.threadID !== threadID) return 'record names another thread';
  if (parts.length !== record.reedIDs.length) return 'parts missing or extra';
  for (let i = 0; i < parts.length; i++) {
    const part = parts[i];
    if (part.id !== record.reedIDs[i]) return `part ${i} is not the listed reed`;
    if (part.userID !== record.userID) return `part ${i} has another author`;
    if (part.thread?.head !== threadID || part.thread?.index !== i) {
      return `part ${i} names another place`;
    }
    if (part.replying || part.echoing) return `part ${i} is also a reply or echo`;
  }
  return null;
}
