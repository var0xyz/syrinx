// Static, UI-only fixtures for the threads mockup. Nothing here touches
// services, repositories or the server.

export const author = { userID: '7@syrinx.local', username: 'wren' };
export const other = { userID: '12@syrinx.local', username: 'kestrel' };
export const third = { userID: '31@syrinx.local', username: 'heron' };

const base = Date.now() - 3 * 60 * 60 * 1000;
const at = (minutes) => new Date(base + minutes * 60 * 1000).toISOString();

export const thread = [
  {
    id: '7@syrinx.local/t-1',
    timestamp: at(0),
    content: 'A few notes on running a #syrinx server for a small book club. Short version: it is boring, and that is the point.',
    stats: { replies: 4, echoes: 2, likes: 11, coverage: 92 },
  },
  {
    id: '7@syrinx.local/t-2',
    timestamp: at(1),
    content: 'The server never holds our posts. It holds *who we are* and the countersignatures. Reed bodies live on our devices.',
    stats: { replies: 1, echoes: 0, likes: 6, coverage: 88 },
  },
  {
    id: '7@syrinx.local/t-3',
    timestamp: at(1),
    content: 'Which means backups are mostly a client thing. Export your keys once, keep them somewhere safe, done.',
    stats: { replies: 2, echoes: 1, likes: 5, coverage: 85 },
  },
  {
    id: '7@syrinx.local/t-4',
    timestamp: at(2),
    content: 'Invites keep it closed. We set `SIGNUP_MODE=invite` and gave everyone two invites. Grew from 6 to 19 in a month.',
    stats: { replies: 0, echoes: 0, likes: 3, coverage: 80 },
  },
  {
    id: '7@syrinx.local/t-5',
    timestamp: at(3),
    content: 'Next up: trying pipes for our monthly #bookpick. Will report back. Questions welcome below.',
    stats: { replies: 3, echoes: 0, likes: 4, coverage: 78 },
  },
];

export const replies = {
  '7@syrinx.local/t-3': [
    { user: other, timestamp: at(20), content: 'Where do you keep the export? Password manager?' },
    { user: third, timestamp: at(41), content: 'Printed QR in a drawer here. Low tech, works.' },
  ],
};

export const feedReeds = [
  { user: third, timestamp: at(-30), content: 'Morning. Coffee, then #bookpick shortlist.' },
  { user: other, timestamp: at(-90), content: 'Finished *The Dispossessed* last night. Still thinking about the wall.' },
];
