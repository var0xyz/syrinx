/**
 * Reed Serializer Class
 * Handles reed creation, serialization, and signature management
 */

import type { ServerSignature, UserSignature } from '$lib/types/api';
import { create } from '@bufbuild/protobuf';
import { UserSignatureSchema } from '$lib/proto/common_pb';
import { generateReedId } from '$lib/utils/id';
import { canonicalReedId } from '$lib/utils/identityRef';
import { buildReedUserPayload } from '$lib/services/signing';

/** What a reply answers: its direct parent and its conversation's root. */
export interface ReedReplying {
  to: string;
  root: string;
}

/** A thread part's place: the head's ID (its own, on the head) and its
 * zero-based index. */
export interface ReedThread {
  head: string;
  index: number;
}

// Signed reed fields. Note: there is intentionally no client-side
// `timestamp` field. The canonical publication date is the server's
// countersigned timestamp, bound into the countersigned payload.
export interface ReedType {
  id: string;
  userID: string;
  replying?: ReedReplying;
  echoing?: string;
  thread?: ReedThread;
  userSignature?: UserSignature;
  serverSignature?: ServerSignature;
  content: string;
  tags: string[];
  mentions: string[];
}

/** Unique hashtags from content (no #), in the casing the author wrote —
 * matching/storage elsewhere is case-insensitive, this is display-only.
 * Deduped case-insensitively, first-seen casing wins. */
export function extractTags(content: string): string[] {
  const hashtagRegex = /(^|\s)#\S+/g;
  const matches = content.match(hashtagRegex);
  if (!matches) return [];
  const seen = new Set<string>();
  const tags: string[] = [];
  for (const match of matches) {
    const tag = match.trim().substring(1);
    const key = tag.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    tags.push(tag);
  }
  return tags;
}

/** ~userID@serverID mention claims from content, canonical form,
 * deduped, self-mentions (matching authorID) dropped. */
export function extractMentions(content: string, authorID: string): string[] {
  const mentionRegex = /~([a-zA-Z0-9]+)@([a-zA-Z0-9]+)/g;
  const seen = new Set<string>();
  for (const match of content.matchAll(mentionRegex)) {
    const [, userID, serverID] = match;
    const canonical = `${userID}@${serverID}`;
    if (canonical === authorID) continue;
    seen.add(canonical);
  }
  return [...seen];
}

/** Rebuild the payload a reed's author signed. */
export function reedSignedPayload(reed: Pick<ReedType, 'id' | 'userID' | 'replying' | 'echoing' | 'thread' | 'content'>): string {
  return buildReedUserPayload(reed);
}

export class Reed {
  private _id: string;
  private _userID: string;
  private _replying: ReedReplying | undefined = undefined;
  private _echoing: string | undefined = undefined;
  private _thread: ReedThread | undefined = undefined;
  private _userSignature: UserSignature | undefined = undefined;
  private _serverSignature: ServerSignature | undefined = undefined;
  private _content: string = '';
  private _tags: string[] = [];
  private _mentions: string[] = [];

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

  get replying(): ReedReplying | undefined {
    return this._replying;
  }

  get content(): string {
    return this._content;
  }

  get tags(): string[] {
    return this._tags;
  }

  get mentions(): string[] {
    return this._mentions;
  }

  set userID(value: string) {
    this._userID = value;
  }

  set id(value: string) {
    this._id = value;
  }

  /** Record the user's detached signature over signedPayload(). */
  setUserSignature(keyId: string, detachedArmor: string): void {
    this._userSignature = create(UserSignatureSchema, {
      id: keyId,
      armor: detachedArmor.trim(),
    });
  }

  applyServerResponse(r: ServerSignature): void {
    this._serverSignature = r;
  }

  set replying(value: ReedReplying | undefined) {
    this._replying = value;
  }

  get echoing(): string | undefined {
    return this._echoing;
  }

  set echoing(value: string) {
    this._echoing = value;
  }

  get thread(): ReedThread | undefined {
    return this._thread;
  }

  set thread(value: ReedThread | undefined) {
    this._thread = value;
  }

  set content(value: string) {
    this._content = value;
    this._tags = extractTags(value);
    this._mentions = extractMentions(value, this._userID);
  }

  /** The payload the author signs. */
  signedPayload(): string {
    return reedSignedPayload(this);
  }

  /**
   * Generate object representation
   */
  asObject(): ReedType {
    return {
      id: this._id,
      userID: this._userID,
      replying: this._replying ? { ...this._replying } : undefined,
      echoing: this._echoing,
      thread: this._thread ? { ...this._thread } : undefined,
      userSignature: this._userSignature ? { ...this._userSignature } : undefined,
      serverSignature: this._serverSignature ? { ...this._serverSignature } : undefined,
      content: this.content,
      tags: this.tags,
      mentions: this.mentions
    };
  }
}
