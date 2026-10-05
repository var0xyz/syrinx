// Static, UI-only fixtures for the chats mockup. Nothing here touches
// services, repositories or the server; state lives in memory only.
import { writable } from 'svelte/store';

export const MAX_LENGTH = 140;

export const me = { userID: '7@syrinx.local', username: 'wren' };

export const people = {
  '12@syrinx.local': { userID: '12@syrinx.local', username: 'kestrel', bio: 'Reads slowly, on purpose. #bookpick curator.' },
  '31@syrinx.local': { userID: '31@syrinx.local', username: 'heron', bio: 'Printed QR codes in a drawer.' },
  '4@other.example': { userID: '4@other.example', username: 'plover', bio: 'Visiting from another server.' },
  '58@syrinx.local': { userID: '58@syrinx.local', username: 'curlew', bio: 'New here.' },
  '63@syrinx.local': { userID: '63@syrinx.local', username: 'dunlin', bio: 'Mostly lurking in #bookpick.' },
};

const now = Date.now();
const ago = (minutes) => new Date(now - minutes * 60 * 1000).toISOString();

/** @param {string} from @param {string} content @param {number} minutes @param {'sent' | 'delivered' | 'read'} [status] */
const msg = (from, content, minutes, status = 'read') => ({ id: `${from}-${minutes}`, from, content, timestamp: ago(minutes), status });

const K = '12@syrinx.local';
const H = '31@syrinx.local';
const P = '4@other.example';
const C = '58@syrinx.local';

export const chats = writable([
  {
    userID: K,
    unread: 2,
    muted: false,
    pinned: true,
    blocked: false,
    deleted: false,
    request: false,
    messages: [
      msg(K, 'Did you finish *The Dispossessed*?', 60 * 26),
      msg(me.userID, 'Last night. Still thinking about the wall.', 60 * 25),
      msg(K, 'Same. I want it as next month’s #bookpick but heron will say it is too long.', 60 * 25 - 3),
      msg(me.userID, 'Heron says everything is too long.', 60 * 25 - 4),
      msg(K, 'Fair 😄', 60 * 25 - 5),
      msg(me.userID, 'Want to co-host the next meetup? I can do the server side, you do the reading list.', 90),
      msg(K, 'Yes! Thursday works for me.', 12),
      msg(K, 'Also: can you send me an invite for my sister? Mine are all used up.', 11),
    ],
  },
  {
    userID: H,
    unread: 0,
    muted: true,
    pinned: false,
    blocked: false,
    deleted: false,
    request: false,
    messages: [
      msg(H, 'Where do you keep your key export?', 60 * 50),
      msg(me.userID, 'Password manager, plus a paper copy.', 60 * 49),
      msg(H, 'Paper copy in a drawer here. Low tech, works.', 60 * 48),
      msg(me.userID, 'Ha, same drawer energy.', 60 * 3, 'delivered'),
    ],
  },
  {
    userID: P,
    unread: 1,
    muted: false,
    pinned: false,
    blocked: false,
    deleted: false,
    request: false,
    messages: [
      msg(me.userID, 'Hi from syrinx.local! Saw your reed about federating book clubs.', 60 * 72),
      msg(P, 'Hello neighbour. Our club would love to swap reading lists.', 60 * 5),
    ],
  },
  {
    userID: C,
    unread: 1,
    muted: false,
    pinned: false,
    blocked: false,
    deleted: false,
    request: true,
    messages: [msg(C, 'Hi! kestrel said you run this server. Is there a guide for new members?', 40)],
  },
]);

/** @param {string} userID @param {Partial<Record<string, unknown>>} patch */
export function patchChat(userID, patch) {
  chats.update((list) => {
    const i = list.findIndex((c) => c.userID === userID);
    if (i === -1) {
      return [...list, { userID, unread: 0, muted: false, pinned: false, blocked: false, deleted: false, request: false, messages: [], ...patch }];
    }
    const next = [...list];
    next[i] = { ...next[i], ...patch };
    return next;
  });
}

/** @param {string} userID @param {string} content */
export function sendMessage(userID, content) {
  chats.update((list) =>
    list.map((c) =>
      c.userID === userID
        ? { ...c, deleted: false, messages: [...c.messages, { id: `${me.userID}-${Date.now()}`, from: me.userID, content, timestamp: new Date().toISOString(), status: 'sent' }] }
        : c
    )
  );
}
