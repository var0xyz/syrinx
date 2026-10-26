// Static, UI-only fixtures for the town halls mockup. Nothing here touches
// services, repositories or the server; state lives in memory only.
import { writable } from 'svelte/store';

export const HOME = 'syrinx.local';
export const CHAT_MAX = 140;
export const TOPIC_MAX = 140;

export const me = { userID: '7@syrinx.local', username: 'wren', admin: true };

export const servers = {
  'syrinx.local': { id: 'syrinx.local', name: 'Syrinx Local', home: true },
  'other.example': { id: 'other.example', name: 'Other Example' },
  'birdhouse.net': { id: 'birdhouse.net', name: 'Birdhouse' },
  'marsh.social': { id: 'marsh.social', name: 'Marsh Social' },
};

/** @param {string} userID */
export const serverOf = (userID) => userID.slice(userID.lastIndexOf('@') + 1);

export const people = {
  '7@syrinx.local': { userID: '7@syrinx.local', username: 'wren', admin: true },
  '12@syrinx.local': { userID: '12@syrinx.local', username: 'kestrel', admin: true },
  '31@syrinx.local': { userID: '31@syrinx.local', username: 'heron', admin: true },
  '2@syrinx.local': { userID: '2@syrinx.local', username: 'osprey', admin: true },
  '44@syrinx.local': { userID: '44@syrinx.local', username: 'sandpiper' },
  '63@syrinx.local': { userID: '63@syrinx.local', username: 'dunlin' },
  '58@syrinx.local': { userID: '58@syrinx.local', username: 'curlew' },
  '70@syrinx.local': { userID: '70@syrinx.local', username: 'stint' },
  '81@syrinx.local': { userID: '81@syrinx.local', username: 'knot' },
  '92@syrinx.local': { userID: '92@syrinx.local', username: 'whimbrel' },
  '15@syrinx.local': { userID: '15@syrinx.local', username: 'redshank' },
  '4@other.example': { userID: '4@other.example', username: 'plover' },
  '9@other.example': { userID: '9@other.example', username: 'lapwing' },
  '21@other.example': { userID: '21@other.example', username: 'turnstone' },
  '3@birdhouse.net': { userID: '3@birdhouse.net', username: 'avocet' },
  '17@birdhouse.net': { userID: '17@birdhouse.net', username: 'godwit' },
  '26@birdhouse.net': { userID: '26@birdhouse.net', username: 'ruff' },
  '33@birdhouse.net': { userID: '33@birdhouse.net', username: 'sanderling' },
  '40@birdhouse.net': { userID: '40@birdhouse.net', username: 'phalarope' },
  '5@marsh.social': { userID: '5@marsh.social', username: 'bittern' },
  '8@marsh.social': { userID: '8@marsh.social', username: 'rail' },
};

/** @param {string} userID */
export const nameOf = (userID) => people[userID]?.username ?? userID;

const now = Date.now();
const MIN = 60 * 1000;

/** The next given weekday at 18:00 local time, at least a day away. */
function nextEvening(daysAhead) {
  const d = new Date(now + daysAhead * 24 * 60 * MIN);
  d.setHours(18, 0, 0, 0);
  return d.toISOString();
}

export const townHalls = writable([
  {
    id: 'th-live',
    state: 'live',
    topic: 'Monthly book club, and opening it to our neighbours',
    description:
      'kestrel proposes moving #bookpick to a monthly call, open to every connected server. Bring your questions; heron runs the floor.',
    organizer: '12@syrinx.local',
    host: '12@syrinx.local',
    moderator: '31@syrinx.local',
    panelists: ['7@syrinx.local', '2@syrinx.local', '3@birdhouse.net'],
    startsAt: new Date(now - 30 * MIN).toISOString(),
    startedAt: new Date(now - 23 * MIN).toISOString(),
    maxMinutes: 90,
    turnSeconds: 120,
    recording: true,
    chat: true,
    capacity: 120,
    scope: 'peers',
    rsvps: 41,
    rsvp: true,
  },
  {
    id: 'th-scheduled',
    state: 'scheduled',
    topic: 'Server costs for next year, and whether to take donations',
    description:
      'The hosting bill doubled when we added the media host. osprey walks through the numbers; then the floor is open. Community only.',
    organizer: '2@syrinx.local',
    host: '2@syrinx.local',
    moderator: '12@syrinx.local',
    panelists: ['7@syrinx.local', '31@syrinx.local', '44@syrinx.local'],
    startsAt: nextEvening(3),
    maxMinutes: 60,
    turnSeconds: 90,
    recording: false,
    chat: true,
    capacity: 0,
    scope: 'community',
    rsvps: 23,
    rsvp: false,
  },
  {
    id: 'th-archived',
    state: 'ended',
    topic: 'Welcome to syrinx.local: ask the admins anything',
    description: 'Our first town hall. What the server keeps, what it does not, and how keys and backups work.',
    organizer: '31@syrinx.local',
    host: '31@syrinx.local',
    moderator: '12@syrinx.local',
    panelists: ['7@syrinx.local', '2@syrinx.local'],
    startsAt: new Date(now - 9 * 24 * 60 * MIN).toISOString(),
    startedAt: new Date(now - 9 * 24 * 60 * MIN + 2 * MIN).toISOString(),
    endedAt: new Date(now - 9 * 24 * 60 * MIN + 49 * MIN).toISOString(),
    maxMinutes: 60,
    turnSeconds: 120,
    recording: true,
    chat: true,
    capacity: 0,
    scope: 'peers',
    rsvps: 37,
    attended: 58,
    attendedServers: 3,
    durationSeconds: 47 * 60 + 12,
  },
]);

/** @param {Record<string, unknown>} th */
export function addTownHall(th) {
  townHalls.update((list) => [...list, th]);
}

/** @param {string} id @param {Record<string, unknown>} patch */
export function patchTownHall(id, patch) {
  townHalls.update((list) => list.map((t) => (t.id === id ? { ...t, ...patch } : t)));
}

// Live town hall: who is connected and what they're doing.
// mic: 'open' (open-mic role), 'floor' (granted), 'off' (audience).
export const liveAttendees = [
  { userID: '12@syrinx.local', role: 'host', muted: false },
  { userID: '31@syrinx.local', role: 'moderator', muted: false },
  { userID: '7@syrinx.local', role: 'panelist', muted: true },
  { userID: '2@syrinx.local', role: 'panelist', muted: false, recording: true },
  { userID: '3@birdhouse.net', role: 'panelist', muted: true, guest: true },
  { userID: '63@syrinx.local', role: 'audience', floor: true },
  { userID: '44@syrinx.local', role: 'audience' },
  { userID: '58@syrinx.local', role: 'audience', hand: 2 },
  { userID: '70@syrinx.local', role: 'audience' },
  { userID: '81@syrinx.local', role: 'audience', reconnecting: true, hand: 4 },
  { userID: '92@syrinx.local', role: 'audience' },
  { userID: '15@syrinx.local', role: 'audience' },
  { userID: '4@other.example', role: 'audience', hand: 1 },
  { userID: '9@other.example', role: 'audience' },
  { userID: '21@other.example', role: 'audience' },
  { userID: '17@birdhouse.net', role: 'audience', hand: 3 },
  { userID: '26@birdhouse.net', role: 'audience' },
  { userID: '33@birdhouse.net', role: 'audience' },
  { userID: '40@birdhouse.net', role: 'audience' },
  { userID: '5@marsh.social', role: 'audience' },
  { userID: '8@marsh.social', role: 'audience' },
];

// Audience members the attendee list summarizes rather than lists.
export const otherListeners = { 'syrinx.local': 31, 'other.example': 5, 'birdhouse.net': 9, 'marsh.social': 2 };

/** @param {string} from @param {string} content @param {number} minutesAgo */
const chat = (from, content, minutesAgo) => ({ id: `${from}-${minutesAgo}`, from, content, at: new Date(now - minutesAgo * MIN).toISOString() });

export const liveChat = [
  chat('9@other.example', 'Hello from other.example 👋 first time joining one of these', 21),
  chat('58@syrinx.local', 'Monthly sounds right. Weekly was a lot.', 18),
  chat('26@birdhouse.net', 'Would we get to pick books too, or only listen?', 15),
  chat('44@syrinx.local', '@ruff kestrel said each server picks one book a quarter', 14),
  chat('92@syrinx.local', 'Is this being recorded? I missed the first five minutes', 9),
  chat('31@syrinx.local', 'It is. The recording goes up after, with the chat and a transcript.', 8),
  chat('5@marsh.social', 'marsh.social is in, we have a reading group of 12 already', 5),
  chat('70@syrinx.local', 'dunlin making the case for poetry, love it', 1),
];

export const incomingChat = [
  ['4@other.example', 'Hand up for a question about time zones'],
  ['33@birdhouse.net', '+1 to poetry months'],
  ['15@syrinx.local', 'Could the call alternate evenings and mornings?'],
  ['8@marsh.social', 'The audio is really clear from here'],
  ['21@other.example', 'Do we need an account on syrinx.local to speak?'],
  ['44@syrinx.local', '@turnstone no, you speak from your own server'],
];

// Archived town hall: the published recording, transcript and chat.
/** @param {number} m @param {number} s */
const t = (m, s) => m * 60 + s;

export const transcript = [
  { at: t(0, 0), speaker: '31@syrinx.local', text: 'Hi everyone, and welcome. This is our first town hall, so bear with us. I am heron, I host tonight, and kestrel is moderating.' },
  { at: t(0, 42), speaker: '12@syrinx.local', text: 'If you want to speak, raise your hand and I will bring you in. wren and osprey are on the panel and can jump in whenever.' },
  { at: t(1, 20), speaker: '31@syrinx.local', text: 'The short version of what this server is: it is a tracker, not a library. Your reeds live on your devices. We keep metadata and signatures.' },
  { at: t(3, 5), speaker: '7@syrinx.local', text: 'And that is why the backup screen nags you. If you lose your device and your export, nobody here can give your reeds back.' },
  { at: t(4, 48), speaker: '31@syrinx.local', text: 'Exactly. Which is also why this recording is not on the server. It was recorded on our phones and published like any other signed content.' },
  { at: t(6, 30), speaker: '12@syrinx.local', text: 'First hand is curlew. Go ahead, you have the floor.' },
  { at: t(6, 41), speaker: '58@syrinx.local', text: 'Thanks. What happens if the server disappears? Do my follows disappear with it?' },
  { at: t(7, 30), speaker: '2@syrinx.local', text: 'Your follows are signed certificates. If we rebuild the database, your client hands them back and we check the signatures. We cannot make them up.' },
  { at: t(10, 2), speaker: '58@syrinx.local', text: 'Okay, that is reassuring. Done, thanks.' },
  { at: t(10, 15), speaker: '12@syrinx.local', text: 'Next is plover, visiting from other.example.' },
  { at: t(10, 26), speaker: '4@other.example', text: 'Hi! Can people on our server follow people here, or is it walled off?' },
  { at: t(11, 40), speaker: '31@syrinx.local', text: 'We are peered with other.example, so yes. Replies, likes, echoes, all of it crosses. A town hall like this one can be opened to you too.' },
  { at: t(15, 12), speaker: '7@syrinx.local', text: 'And when you listen from other.example, your server gets one stream and passes it to you. We do not send one per listener.' },
  { at: t(18, 50), speaker: '12@syrinx.local', text: 'We have a few hands. dunlin, you are up. Two minutes per turn tonight.' },
  { at: t(19, 2), speaker: '63@syrinx.local', text: 'What can admins actually see? Can you read my chats?' },
  { at: t(20, 10), speaker: '2@syrinx.local', text: 'No. The server never sees reed content, only the signature over it. Admin actions take two of us, and every one of them is in the public ledger.' },
  { at: t(24, 33), speaker: '63@syrinx.local', text: 'Two admins for everything? Even a kick?' },
  { at: t(24, 50), speaker: '2@syrinx.local', text: 'Even a kick. And the second one can not be someone the first one invited.' },
  { at: t(28, 4), speaker: '12@syrinx.local', text: 'Time on that turn. Thanks dunlin. heron, want to talk about invites?' },
  { at: t(28, 20), speaker: '31@syrinx.local', text: 'Sure. Signups are invite only. Each of you has a few invites, and the invite tree is how we keep this a community and not a crowd.' },
  { at: t(34, 0), speaker: '7@syrinx.local', text: 'If someone you invited misbehaves, nothing happens to you. The tree is for context, not for blame.' },
  { at: t(38, 45), speaker: '12@syrinx.local', text: 'Last hand tonight, godwit from birdhouse.net.' },
  { at: t(39, 0), speaker: '17@birdhouse.net', text: 'Would you do these regularly? It is nice to hear the people behind a server.' },
  { at: t(40, 18), speaker: '31@syrinx.local', text: 'That is the plan. Monthly, probably. RSVP and you will get a reminder before each one.' },
  { at: t(44, 30), speaker: '12@syrinx.local', text: 'That is time. Thank you all for coming. The recording and this transcript will be up in a day or so.' },
  { at: t(46, 55), speaker: '31@syrinx.local', text: 'Goodnight everyone.' },
];

export const archiveChat = [
  { at: t(0, 30), from: '58@syrinx.local', content: 'Hi all!' },
  { at: t(2, 10), from: '9@other.example', content: 'Joining from other.example, hello' },
  { at: t(5, 2), from: '63@syrinx.local', content: 'Recording on phones is a neat trick' },
  { at: t(8, 15), from: '44@syrinx.local', content: 'The follow certificates thing is the best part of this whole design' },
  { at: t(12, 40), from: '17@birdhouse.net', content: 'Hand up, hope I get a turn' },
  { at: t(16, 0), from: '4@other.example', content: 'Thanks for answering!' },
  { at: t(21, 33), from: '70@syrinx.local', content: 'Two admins for every action, I did not know that' },
  { at: t(30, 12), from: '26@birdhouse.net', content: 'Where is the public ledger?' },
  { at: t(30, 50), from: '12@syrinx.local', content: '@ruff Network → Ledger, anyone can read it' },
  { at: t(41, 0), from: '17@birdhouse.net', content: 'Monthly would be great 🙌' },
  { at: t(46, 30), from: '58@syrinx.local', content: 'Goodnight, thank you!' },
];

// Each admin present recorded on their own device; the organizer picked one.
export const recordings = [
  { by: '31@syrinx.local', covered: [[0, 1.0]], chosen: true, sizeMB: 5.7, kept: true },
  { by: '2@syrinx.local', covered: [[0, 0.41], [0.44, 1.0]], gap: '1m 25s gap at 19:20 (phone call)', sizeMB: 5.5, kept: true },
  { by: '7@syrinx.local', covered: [[0.06, 1.0]], gap: 'Joined 2m 50s late', sizeMB: 5.4, kept: false },
];

export const backup = { holders: 41, chunks: 23, copies: 3, mine: 6, mineMB: 1.5, capMB: 2, admins: 3, availability: '99.4%' };
