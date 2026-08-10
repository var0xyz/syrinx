#!/usr/bin/env node
// Standalone test for the pure logic behind revocation handling:
//   - spa/src/lib/utils/keyCheckThrottle.ts (sessionStorage re-check window)
//   - the isKeyValidAt timestamp comparison in spa/src/lib/verifiers/index.ts
//
// The SPA does not have a JS unit-test framework installed (see
// test-signing.mjs); this script mirrors that convention: plain node,
// logic inlined here to match the real files exactly, no build step.

let failed = 0;

function ok(name) {
  console.log(`ok   ${name}`);
}
function fail(name, extra) {
  failed++;
  console.error(`FAIL ${name}`);
  if (extra) console.error(' ', extra);
}

// --- keyCheckThrottle.ts, inlined against a minimal sessionStorage stub ---

class MemoryStorage {
  constructor() {
    this.map = new Map();
  }
  getItem(k) {
    return this.map.has(k) ? this.map.get(k) : null;
  }
  setItem(k, v) {
    this.map.set(k, String(v));
  }
}

const PREFIX = 'keyChecked:';
const WINDOW_MS = 60_000;

function makeThrottle(storage, now) {
  return {
    shouldRecheck(fingerprint) {
      const stamped = storage.getItem(PREFIX + fingerprint);
      if (!stamped) return true;
      const at = Number(stamped);
      if (Number.isNaN(at)) return true;
      return now() - at >= WINDOW_MS;
    },
    markChecked(fingerprint) {
      storage.setItem(PREFIX + fingerprint, String(now()));
    },
  };
}

(function testThrottleFirstCheckAlwaysRechecks() {
  const storage = new MemoryStorage();
  let t = 1_000_000;
  const throttle = makeThrottle(storage, () => t);

  if (throttle.shouldRecheck('FP1') !== true) {
    return fail('throttle: first check triggers recheck');
  }
  ok('throttle: first check triggers recheck');
})();

(function testThrottleWithinWindowSkips() {
  const storage = new MemoryStorage();
  let t = 1_000_000;
  const throttle = makeThrottle(storage, () => t);

  throttle.markChecked('FP1');
  t += 30_000; // 30s later, within the 60s window
  if (throttle.shouldRecheck('FP1') !== false) {
    return fail('throttle: within window does not recheck');
  }
  ok('throttle: within window does not recheck');
})();

(function testThrottlePastWindowRechecks() {
  const storage = new MemoryStorage();
  let t = 1_000_000;
  const throttle = makeThrottle(storage, () => t);

  throttle.markChecked('FP1');
  t += 60_000; // exactly at the window boundary
  if (throttle.shouldRecheck('FP1') !== true) {
    return fail('throttle: at/past window triggers recheck');
  }
  ok('throttle: at/past window triggers recheck');
})();

(function testThrottleIndependentPerFingerprint() {
  const storage = new MemoryStorage();
  let t = 1_000_000;
  const throttle = makeThrottle(storage, () => t);

  throttle.markChecked('FP1');
  if (throttle.shouldRecheck('FP2') !== true) {
    return fail('throttle: unrelated fingerprint unaffected');
  }
  ok('throttle: unrelated fingerprint unaffected');
})();

// --- isKeyValidAt timestamp comparison, inlined ---
// Mirrors verifiers/index.ts: valid = atISO < revocation.serverSignature.timestamp

function isValidAt(atISO, revokedAtISO) {
  return Date.parse(atISO) < Date.parse(revokedAtISO);
}

(function testContentSignedBeforeRevocationIsValid() {
  const valid = isValidAt('2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z');
  if (valid !== true) return fail('isKeyValidAt: signed before revocation is valid');
  ok('isKeyValidAt: signed before revocation is valid');
})();

(function testContentSignedAtRevocationIsInvalid() {
  const valid = isValidAt('2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z');
  if (valid !== false) return fail('isKeyValidAt: signed at revocation instant is invalid');
  ok('isKeyValidAt: signed at revocation instant is invalid');
})();

(function testContentSignedAfterRevocationIsInvalid() {
  const valid = isValidAt('2026-01-03T00:00:00Z', '2026-01-02T00:00:00Z');
  if (valid !== false) return fail('isKeyValidAt: signed after revocation is invalid');
  ok('isKeyValidAt: signed after revocation is invalid');
})();

if (failed > 0) {
  console.error(`\n${failed} test(s) failed`);
  process.exit(1);
}
console.log('\nAll tests passed');
