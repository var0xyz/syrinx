// See https://kit.svelte.dev/docs/types#app
// for information about these interfaces
declare global {
  namespace App {
    // interface Error {}
    // interface Locals {}
    // interface PageData {}
    // interface Platform {}
  }

  /** Git commit hash of this build — injected by vite.config.ts's define block. */
  const __APP_VERSION__: string;
}

export {};

