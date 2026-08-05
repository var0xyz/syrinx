/**
 * Deterministic multi-color geometric identicon from SHA-256(userID@serverID).
 *
 * Colors: root hue from the hash + fixed “scale” intervals (like scale degrees).
 * Shape: hash also picks motif, density, and how many scale degrees are active,
 * so avatars differ in silhouette — not only in recolored noise.
 */

export type IdenticonModel = {
  /** Grid edge length (square). */
  size: number;
  /** Background fill. */
  background: string;
  /**
   * size×size cells; null = show background, otherwise a CSS color.
   * Horizontally mirrored.
   */
  cells: (string | null)[][];
};

/** Relative swatch: hue degrees, sat delta, light delta from the root. */
type ScaleDegree = { h: number; s: number; l: number };

type Motif =
  | 'speckle'
  | 'blocks'
  | 'stripes-h'
  | 'stripes-v'
  | 'diamond'
  | 'quarters'
  | 'orbit';

const MOTIFS: Motif[] = [
  'speckle',
  'blocks',
  'stripes-h',
  'stripes-v',
  'diamond',
  'quarters',
  'orbit',
];

/**
 * Named color “scales”. Intervals are fixed; the root comes from the hash.
 */
const SCALES: ScaleDegree[][] = [
  // Analogous cluster
  [
    { h: 0, s: 0, l: 0 },
    { h: 18, s: 4, l: 4 },
    { h: -18, s: 2, l: -4 },
    { h: 36, s: -6, l: 8 },
    { h: -36, s: -4, l: -6 },
    { h: 54, s: 8, l: -2 },
    { h: -54, s: 0, l: 6 },
    { h: 72, s: -8, l: 2 },
  ],
  // Complementary + splits
  [
    { h: 0, s: 0, l: 0 },
    { h: 180, s: -4, l: 2 },
    { h: 150, s: 2, l: -4 },
    { h: 210, s: 4, l: 4 },
    { h: 30, s: -6, l: 6 },
    { h: -30, s: 0, l: -6 },
    { h: 165, s: 6, l: 0 },
    { h: 195, s: -2, l: 8 },
  ],
  // Triadic
  [
    { h: 0, s: 0, l: 0 },
    { h: 120, s: 0, l: 2 },
    { h: 240, s: 0, l: -2 },
    { h: 20, s: -8, l: 6 },
    { h: 140, s: 4, l: -4 },
    { h: 260, s: 2, l: 4 },
    { h: -20, s: 6, l: 0 },
    { h: 100, s: -4, l: 8 },
  ],
  // Warm steps
  [
    { h: 0, s: 0, l: 0 },
    { h: 25, s: 4, l: 2 },
    { h: 50, s: -2, l: 6 },
    { h: 85, s: 6, l: -4 },
    { h: 160, s: -6, l: 0 },
    { h: 185, s: 2, l: 4 },
    { h: 210, s: 0, l: -6 },
    { h: -40, s: 4, l: 8 },
  ],
  // Cool steps
  [
    { h: 0, s: 0, l: -2 },
    { h: -22, s: 2, l: 0 },
    { h: -45, s: -4, l: 4 },
    { h: 80, s: 0, l: 6 },
    { h: 130, s: 6, l: -4 },
    { h: 175, s: -2, l: 2 },
    { h: 220, s: 4, l: 0 },
    { h: 280, s: -6, l: 8 },
  ],
];

export async function sha256Utf8(text: string): Promise<Uint8Array> {
  const data = new TextEncoder().encode(text);
  const digest = await crypto.subtle.digest('SHA-256', data);
  return new Uint8Array(digest);
}

function hsl(h: number, s: number, l: number): string {
  const sat = Math.min(100, Math.max(0, s));
  const light = Math.min(100, Math.max(0, l));
  return `hsl(${((h % 360) + 360) % 360} ${sat}% ${light}%)`;
}

/** Pull bytes from the digest, wrapping so we can consume freely. */
function byteStream(digest: Uint8Array): () => number {
  let i = 0;
  return () => {
    const v = digest[i % digest.length];
    i += 1;
    return v;
  };
}

const GRID = 11;

/**
 * Build palette from root HSL + a fixed scale (interval set).
 */
export function paletteFromRoot(
  rootH: number,
  rootS: number,
  rootL: number,
  scale: ScaleDegree[]
): string[] {
  return scale.map((deg) => hsl(rootH + deg.h, rootS + deg.s, rootL + deg.l));
}

function emptyGrid(size: number): (string | null)[][] {
  return Array.from({ length: size }, () => Array.from({ length: size }, () => null));
}

function paintMirror(
  cells: (string | null)[][],
  row: number,
  col: number,
  fill: string | null
): void {
  const size = cells.length;
  cells[row][col] = fill;
  cells[row][size - 1 - col] = fill;
}

function pickColor(next: () => number, colors: string[], emptyChance: number): string | null {
  // emptyChance in 0..255; higher → more background
  if (next() < emptyChance) return null;
  return colors[next() % colors.length];
}

function paintSpeckle(
  cells: (string | null)[][],
  colors: string[],
  next: () => number,
  emptyChance: number
): void {
  const size = cells.length;
  const mid = Math.floor(size / 2);
  for (let row = 0; row < size; row++) {
    for (let col = 0; col <= mid; col++) {
      paintMirror(cells, row, col, pickColor(next, colors, emptyChance));
    }
  }
}

function paintBlocks(
  cells: (string | null)[][],
  colors: string[],
  next: () => number,
  emptyChance: number
): void {
  const size = cells.length;
  const mid = Math.floor(size / 2);
  const block = 2 + (next() % 2); // 2 or 3
  for (let row = 0; row < size; row += block) {
    for (let col = 0; col <= mid; col += block) {
      const fill = pickColor(next, colors, emptyChance);
      for (let dr = 0; dr < block && row + dr < size; dr++) {
        for (let dc = 0; dc < block && col + dc <= mid; dc++) {
          paintMirror(cells, row + dr, col + dc, fill);
        }
      }
    }
  }
}

function paintStripesH(
  cells: (string | null)[][],
  colors: string[],
  next: () => number,
  emptyChance: number
): void {
  const size = cells.length;
  const mid = Math.floor(size / 2);
  const thickness = 1 + (next() % 2);
  for (let row = 0; row < size; row += thickness) {
    const fill = pickColor(next, colors, emptyChance);
    for (let dr = 0; dr < thickness && row + dr < size; dr++) {
      for (let col = 0; col <= mid; col++) {
        paintMirror(cells, row + dr, col, fill);
      }
    }
  }
}

function paintStripesV(
  cells: (string | null)[][],
  colors: string[],
  next: () => number,
  emptyChance: number
): void {
  const size = cells.length;
  const mid = Math.floor(size / 2);
  const thickness = 1 + (next() % 2);
  for (let col = 0; col <= mid; col += thickness) {
    const fill = pickColor(next, colors, emptyChance);
    for (let dc = 0; dc < thickness && col + dc <= mid; dc++) {
      for (let row = 0; row < size; row++) {
        paintMirror(cells, row, col + dc, fill);
      }
    }
  }
}

function paintDiamond(
  cells: (string | null)[][],
  colors: string[],
  next: () => number,
  emptyChance: number
): void {
  const size = cells.length;
  const mid = Math.floor(size / 2);
  const cx = mid;
  const cy = mid;
  // Band → color (or empty), decided once per band index.
  const maxBand = mid + mid;
  const bandFill: (string | null)[] = [];
  for (let b = 0; b <= maxBand; b++) {
    bandFill.push(pickColor(next, colors, emptyChance));
  }
  for (let row = 0; row < size; row++) {
    for (let col = 0; col <= mid; col++) {
      const band = Math.abs(row - cy) + Math.abs(col - cx);
      paintMirror(cells, row, col, bandFill[band] ?? null);
    }
  }
}

function paintQuarters(
  cells: (string | null)[][],
  colors: string[],
  next: () => number,
  emptyChance: number
): void {
  const size = cells.length;
  const mid = Math.floor(size / 2);
  // Four regions on the left half (top/bottom × inner/outer), then mirrored.
  const fills = [
    pickColor(next, colors, emptyChance),
    pickColor(next, colors, emptyChance),
    pickColor(next, colors, emptyChance),
    pickColor(next, colors, emptyChance),
  ];
  // Optional center cross / inset speckles for variety within the big shapes.
  const inset = next() % 3; // 0 none, 1 punch holes, 2 overlay dots
  for (let row = 0; row < size; row++) {
    for (let col = 0; col <= mid; col++) {
      const qi = (row < mid ? 0 : 2) + (col < Math.floor(mid / 2) ? 0 : 1);
      let fill = fills[qi];
      if (inset === 1 && next() < 90) fill = null;
      if (inset === 2 && next() < 70) fill = colors[next() % colors.length];
      paintMirror(cells, row, col, fill);
    }
  }
}

function paintOrbit(
  cells: (string | null)[][],
  colors: string[],
  next: () => number,
  emptyChance: number
): void {
  const size = cells.length;
  const mid = Math.floor(size / 2);
  const cx = mid;
  const cy = mid;
  const maxR = Math.ceil(Math.hypot(mid, mid));
  const ringFill: (string | null)[] = [];
  for (let r = 0; r <= maxR; r++) {
    ringFill.push(pickColor(next, colors, emptyChance));
  }
  for (let row = 0; row < size; row++) {
    for (let col = 0; col <= mid; col++) {
      const ring = Math.round(Math.hypot(row - cy, col - cx));
      paintMirror(cells, row, col, ringFill[ring] ?? null);
    }
  }
}

function paintMotif(
  motif: Motif,
  cells: (string | null)[][],
  colors: string[],
  next: () => number,
  emptyChance: number
): void {
  switch (motif) {
    case 'blocks':
      paintBlocks(cells, colors, next, emptyChance);
      break;
    case 'stripes-h':
      paintStripesH(cells, colors, next, emptyChance);
      break;
    case 'stripes-v':
      paintStripesV(cells, colors, next, emptyChance);
      break;
    case 'diamond':
      paintDiamond(cells, colors, next, emptyChance);
      break;
    case 'quarters':
      paintQuarters(cells, colors, next, emptyChance);
      break;
    case 'orbit':
      paintOrbit(cells, colors, next, emptyChance);
      break;
    case 'speckle':
    default:
      paintSpeckle(cells, colors, next, emptyChance);
      break;
  }
}

/**
 * Build a colorful mirrored identicon from a 32-byte digest.
 */
export function identiconFromDigest(digest: Uint8Array): IdenticonModel {
  if (digest.length < 32) {
    throw new Error('digest must be at least 32 bytes');
  }

  const next = byteStream(digest);

  const rootH = (next() << 8) | next();
  const rootS = 58 + (next() % 22);
  const rootL = 46 + (next() % 16);
  const scale = SCALES[next() % SCALES.length];
  const fullPalette = paletteFromRoot(rootH, rootS, rootL, scale);

  // Few active degrees → clearer shapes; not all eight every time.
  const activeCount = 2 + (next() % 4); // 2..5
  const colors = fullPalette.slice(0, activeCount);

  const motif = MOTIFS[next() % MOTIFS.length];
  // emptyChance 40..160 of 255 → sparse ↔ dense silhouettes
  const emptyChance = 40 + (next() % 121);

  const background = hsl(
    rootH + (scale[1]?.h ?? 0),
    28 + (next() % 14),
    14 + (next() % 8)
  );

  const size = GRID;
  const cells = emptyGrid(size);
  paintMotif(motif, cells, colors, next, emptyChance);

  return { size, background, cells };
}

/** Canonical identicon input: scoped to one server instance. */
export function identiconIdentity(userID: string, serverID: string): string {
  return `${userID}@${serverID}`;
}

export async function identiconForUser(userID: string, serverID: string): Promise<IdenticonModel> {
  return identiconFromDigest(await sha256Utf8(identiconIdentity(userID, serverID)));
}

/** Stable string for tests. */
export function identiconFingerprint(model: IdenticonModel): string {
  const body = model.cells
    .map((row) => row.map((c) => c ?? '_').join(','))
    .join(';');
  return `${model.size}|${model.background}|${body}`;
}
