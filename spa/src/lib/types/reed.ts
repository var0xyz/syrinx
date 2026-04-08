/**
 * Reed Serializer Class
 * Handles reed creation, serialization, and signature management
 */

import { v7 as uuidv7 } from 'uuid';
import { Uuid25 } from 'uuid25';

export type Headers = {
  id: string;
  author: string;
  serverId: string;
  origin: string;
  fingerprint: string;
  algorithm: string;
  userSignature?: string;
  serverAlgorithm?: string;
  serverSignature?: string;
  serverSignedAt?: string;
  timestamp: string;
  format: string;
  replying?: string;
  echoing?: string;
};

export interface ReedType {
  headers: Headers;
  content: string;
  tags: string[];
}

export class Reed {
  private _headers: Headers;
  private _content: string = '';
  private _tags: string[] = [];

  constructor() {
    // Auto-generate id using UUID v7 for time-based ordering, encoded as 25-char string
    const id = Uuid25.parse(uuidv7()).value;

    // Auto-populate origin, author, and server
    const origin = typeof window !== 'undefined' ? window.location.origin : '';
    const author = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') || '' : '';
    const serverId = typeof localStorage !== 'undefined' ? localStorage.getItem('serverId') || '' : '';

    this._headers = {
      algorithm: 'PGP+base64',
      author,
      serverId,
      origin,
      id,
      fingerprint: '',
      timestamp: new Date().toISOString(),
      format: 'markdown'
    };
  }

  // Getters for header fields
  get algorithm(): string {
    return this._headers.algorithm;
  }

  get author(): string {
    return this._headers.author;
  }

  get key(): string {
    return this._headers.fingerprint;
  }

  get serverId(): string {
    return this._headers.serverId;
  }

  get origin(): string {
    return this._headers.origin;
  }

  get id(): string {
    return this._headers.id;
  }

  get fingerprint(): string {
    return this._headers.fingerprint;
  }

  get userSignature(): string {
    return this._headers.userSignature;
  }

  get serverAlgorithm(): string {
    return this._headers.serverAlgorithm;
  }

  get serverSignature(): string {
    return this._headers.serverSignature;
  }

  get serverSignedAt(): string {
    return this._headers.serverSignedAt;
  }

  get replying(): string | undefined {
    return this._headers.replying;
  }

  get content(): string {
    return this._content;
  }

  get tags(): string[] {
    return this._tags;
  }

  // Setters for header fields
  set algorithm(value: string) {
    this._headers.algorithm = value;
  }

  set author(value: string) {
    this._headers.author = value;
  }

  set key(value: string) {
    this._headers.fingerprint = value;
  }

  set origin(value: string) {
    this._headers.origin = value;
  }

  set id(value: string) {
    this._headers.id = value;
  }

  set fingerprint(value: string) {
    this._headers.fingerprint = value;
  }

  set userSignature(value: string) {
    this._headers.userSignature = btoa(value.trim()).trim();
  }

  set serverAlgorithm(value: string) {
    this._headers.serverAlgorithm = value;
  }

  set serverSignature(value: string) {
    this._headers.serverSignature = value;
  }

  set serverSignedAt(value: string) {
    this._headers.serverSignedAt = value;
  }

  set replying(value: string) {
    this._headers.replying = value;
  }

  get echoing(): string | undefined {
    return this._headers.echoing;
  }

  set echoing(value: string) {
    this._headers.echoing = value;
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
   * Generate markdown representation with alphabetically ordered headers
   */
  asMarkdown(): string {
    return (
        "---\n" +
        Object.keys(this._headers)
            .filter(key => !!this._headers[key])
            .sort()
            .map(key => `${key}: ${this._headers[key]}`)
            .join('\n') +
        "\n---\n" +
        this.content
    );
  }

  /**
   * Generate object representation
   */
  asObject(): ReedType {
    return {
      headers: { ...this._headers },
      content: this.content,
      tags: this.tags
    };
  }
}
