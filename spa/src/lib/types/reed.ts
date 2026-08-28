/**
 * Reed Serializer Class
 * Handles reed creation, serialization, and signature management
 */

import type { ServerSignature, UserSignature } from '$lib/types/api';
import { generateReedId } from '$lib/utils/id';
import { canonicalReedId } from '$lib/utils/identityRef';

// Reed markdown frontmatter fields. Note: there is intentionally no client-side
// `timestamp` field. The canonical publication date is the server's
// countersigned timestamp, bound into the countersigned payload.
export interface ReedType {
  id: string;
  userID: string;
  replying?: string;
  echoing?: string;
  threadId?: string;
  userSignature?: UserSignature;
  serverSignature?: ServerSignature;
  content: string;
  tags: string[];
}

/** Reconstruct the signed markdown payload from a reed object. */
export function reedAsMarkdown(reed: Pick<ReedType, 'id' | 'userID' | 'replying' | 'echoing' | 'threadId' | 'content'>): string {
  const headers: Record<string, string> = {
    id: reed.id,
    userID: reed.userID,
  };
  if (reed.replying) headers.replying = reed.replying;
  if (reed.echoing) headers.echoing = reed.echoing;
  if (reed.threadId) headers.threadId = reed.threadId;

  return (
    "---\n" +
    Object.keys(headers)
      .sort()
      .map(key => `${key}: ${headers[key]}`)
      .join('\n') +
    "\n---\n" +
    reed.content
  );
}

export class Reed {
  private _id: string;
  private _userID: string;
  private _replying: string | undefined = undefined;
  private _echoing: string | undefined = undefined;
  private _threadId: string | undefined = undefined;
  private _userSignature: UserSignature | undefined = undefined;
  private _serverSignature: ServerSignature | undefined = undefined;
  private _content: string = '';
  private _tags: string[] = [];

  constructor() {
    // Auto-populate userID (already canonical: userID@serverID).
    this._userID = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') || '' : '';

    // Canonical id (userID/uuid) — same composition as everywhere else in
    // the app; author lists sort by the UUIDv7 suffix.
    this._id = canonicalReedId({ userID: this._userID, id: generateReedId() });
  }

  get userID(): string {
    return this._userID;
  }

  get id(): string {
    return this._id;
  }

  get userSignature(): UserSignature | undefined {
    return this._userSignature;
  }

  get serverSignature(): ServerSignature | undefined {
    return this._serverSignature;
  }

  get replying(): string | undefined {
    return this._replying;
  }

  get content(): string {
    return this._content;
  }

  get tags(): string[] {
    return this._tags;
  }

  set userID(value: string) {
    this._userID = value;
  }

  set id(value: string) {
    this._id = value;
  }

  /** Record the user's detached signature over asMarkdown(). */
  setUserSignature(fingerprint: string, detachedArmor: string): void {
    this._userSignature = {
      fingerprint,
      armor: btoa(detachedArmor.trim()).trim(),
    };
  }

  applyServerResponse(r: { id: string; timestamp: string; armor: string }): void {
    this._serverSignature = {
      id: r.id,
      armor: r.armor,
      timestamp: r.timestamp,
    };
  }

  set replying(value: string) {
    this._replying = value;
  }

  get echoing(): string | undefined {
    return this._echoing;
  }

  set echoing(value: string) {
    this._echoing = value;
  }

  get threadId(): string | undefined {
    return this._threadId;
  }

  set threadId(value: string | undefined) {
    this._threadId = value;
  }

  set content(value: string) {
    this._content = value;
    this._tags = this.extractTags(value);
  }

  /**
   * Extract hashtags from content
   * Returns normalized (lowercase) unique tags without the # prefix
   */
  private extractTags(content: string): string[] {
    const hashtagRegex = /(^|\s)#\S+/g;
    const matches = content.match(hashtagRegex);

    if (!matches) {
      return [];
    }

    // Remove # prefix, normalize to lowercase, and remove duplicates
    const tags = matches.map(tag => tag.trim().substring(1).toLowerCase());
    return [...new Set(tags)];
  }

  /**
   * Generate markdown representation with alphabetically ordered frontmatter
   */
  asMarkdown(): string {
    return reedAsMarkdown(this);
  }

  /**
   * Generate object representation
   */
  asObject(): ReedType {
    return {
      id: this._id,
      userID: this._userID,
      replying: this._replying,
      echoing: this._echoing,
      threadId: this._threadId,
      userSignature: this._userSignature ? { ...this._userSignature } : undefined,
      serverSignature: this._serverSignature ? { ...this._serverSignature } : undefined,
      content: this.content,
      tags: this.tags
    };
  }
}
