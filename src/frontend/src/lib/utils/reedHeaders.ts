/** The signed headers of a reed, before ordering. */
export interface HeaderedReed {
  id: string;
  userID: string;
  replying?: { to: string; root: string } | null;
  echoing?: string | null;
  thread?: { head: string; index: number } | null;
}

/** Signed header map; `replying` and `thread` flatten to dotted keys. */
export function reedSignedHeaders(reed: HeaderedReed): Record<string, string> {
  const headers: Record<string, string> = { id: reed.id, userID: reed.userID };
  if (reed.replying) {
    headers['replying.root'] = reed.replying.root;
    headers['replying.to'] = reed.replying.to;
  }
  if (reed.echoing) headers.echoing = reed.echoing;
  if (reed.thread) {
    headers['thread.head'] = reed.thread.head;
    headers['thread.index'] = String(reed.thread.index);
  }
  return headers;
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

/** The bytes an author signs: sorted headers as frontmatter, then content. */
export function signedReedMarkdown(reed: HeaderedReed & { content: string }): string {
  const headers = reedSignedHeaders(reed);
  return (
    '---\n' +
    Object.keys(headers)
      .sort()
      .map((key) => `${key}: ${headers[key]}`)
      .join('\n') +
    '\n---\n' +
    reed.content
  );
}
