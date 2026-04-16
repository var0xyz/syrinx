/**
 * Reed Serializer Class
 * Handles reed creation, serialization, and signature management
 */

import { v7 as uuidv7 } from 'uuid';
import { Uuid25 } from 'uuid25';

export type Server = {
  id: string;
  timestamp: string;
  signature: string;
  algorithm: string;
};

export type Headers = {
  id: string;
  author: string;
  origin: string;
  fingerprint: string;
  algorithm: string;
  timestamp: string;
  format: string;
  replying?: string;
  echoing?: string;
};

export interface ReedType {
  headers: Headers;
  server?: Server;
  signature?: string;
  content: string;
  tags: string[];
}

export class Reed {
  private _headers: Headers;
  private _server: Server | undefined = undefined;
  private _signature: string | undefined = undefined;
  private _content: string = '';
  private _tags: string[] = [];

  constructor() {
    // Auto-generate id using UUID v7 for time-based ordering, encoded as 25-char string
    const id = Uuid25.parse(uuidv7()).value;

    // Auto-populate origin and author
    const origin = typeof window !== 'undefined' ? window.location.origin : '';
    const author = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') || '' : '';

    this._headers = {
      algorithm: 'PGP+base64',
      author,
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

  get origin(): string {
    return this._headers.origin;
  }

  get id(): string {
    return this._headers.id;
  }

  get fingerprint(): string {
    return this._headers.fingerprint;
  }

  get signature(): string {
    return this._signature;
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

  set signature(value: string) {
    this._signature = btoa(value.trim()).trim();
  }

  applyServerResponse(r: { id: string; timestamp: string; algorithm: string; signature: string }): void {
    this._server = {
      id: r.id,
      algorithm: r.algorithm,
      signature: r.signature,
      timestamp: r.timestamp,
    };
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
      server: this._server ? { ...this._server } : undefined,
      signature: this._signature,
      content: this.content,
      tags: this.tags
    };
  }
}
