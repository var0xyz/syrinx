<script lang="ts">
  import { goto } from '$app/navigation';
  import { cryptoService } from '$lib/services/crypto';
  import { dbService } from '$lib/services/db';

  let files: FileList | null = null;
  let password = '';
  let importing = false;
  let error = '';
  let success = false;

  $: file = files?.[0] ?? null;
  $: canImport = file !== null && password.length > 0;

  async function handleImport() {
    if (!file) return;
    importing = true;
    error = '';

    try {
      // 1. Read file bytes
      const fileBytes = new Uint8Array(await file.arrayBuffer());

      // 2. Decrypt with provided password
      let decrypted: Uint8Array;
      try {
        decrypted = await cryptoService.decryptBackup(fileBytes, password);
      } catch {
        throw new Error('Failed to decrypt backup. Check that the password is correct and the file is not corrupted.');
      }

      // 3. Decompress gzip
      const decompressed = await decompressGzip(decrypted);

      // 4. Parse JSON
      const backup = JSON.parse(new TextDecoder().decode(decompressed));

      // 5. Extract identity from backup
      const ls: Record<string, string> = backup.localStorage ?? {};
      const userId: string = ls['userId'];
      const keyFingerprint: string = ls['keyFingerprint'];
      const keyPassphrase: string = ls['keyPassphrase'];

      const privateKeysTable = (backup.indexedDB?.tables ?? []).find((t: any) => t.name === 'privateKeys');
      const privateKeyEntry = privateKeysTable?.items?.find((k: any) => k.fingerprint === keyFingerprint);

      if (!userId || !keyFingerprint || !keyPassphrase || !privateKeyEntry?.armor) {
        throw new Error('Invalid backup file: missing required identity data.');
      }

      // 6. Check if the user already exists on this server
      const exists = await checkUserExists(userId, keyFingerprint, keyPassphrase, privateKeyEntry.armor);
      if (exists) {
        throw new Error('This account already exists on this server. Multiple devices are currently not supported.');
      }

      // 7. Restore localStorage
      for (const [key, value] of Object.entries(ls)) {
        localStorage.setItem(key, value);
      }

      // 8. Restore IndexedDB tables
      await dbService.init();
      for (const table of (backup.indexedDB?.tables ?? [])) {
        for (const item of (table.items ?? [])) {
          try {
            await dbService.put(table.name, item as any);
          } catch (e) {
            console.error(`Failed to restore item in table ${table.name}:`, e);
          }
        }
      }

      success = true;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Import failed. Please try again.';
    } finally {
      importing = false;
    }
  }

  async function checkUserExists(
    userId: string,
    fingerprint: string,
    passphrase: string,
    privateKeyArmor: string
  ): Promise<boolean> {
    const timestamp = Math.floor(Date.now() / 1000).toString();
    const path = `/api/users/${userId}`;

    // Build canonical request string matching the server's expected format
    const canonical = [`GET ${path}`, '', '', '', timestamp].join('\n');
    const signature = await cryptoService.signMessage(canonical, privateKeyArmor, passphrase);
    const signatureB64 = btoa(signature.trim());

    const res = await fetch(path, {
      method: 'GET',
      headers: {
        'X-Syrinx-User-Id': userId,
        'X-Syrinx-Fingerprint': fingerprint,
        'X-Syrinx-Algorithm': 'PGP+base64',
        'X-Syrinx-Signature-Scope': 'body',
        'X-Syrinx-Timestamp': timestamp,
        'X-Syrinx-Signature': signatureB64,
      }
    });

    if (res.status === 200) return true;
    if (res.status === 404) return false;
    throw new Error(`Unexpected server response while checking account: HTTP ${res.status}`);
  }

  async function decompressGzip(data: Uint8Array): Promise<Uint8Array> {
    const stream = new DecompressionStream('gzip');
    const writer = stream.writable.getWriter();
    writer.write(data);
    writer.close();

    const chunks: Uint8Array[] = [];
    const reader = stream.readable.getReader();
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        chunks.push(value);
      }
    } finally {
      reader.releaseLock();
    }

    const total = chunks.reduce((sum, c) => sum + c.length, 0);
    const result = new Uint8Array(total);
    let offset = 0;
    for (const chunk of chunks) {
      result.set(chunk, offset);
      offset += chunk.length;
    }
    return result;
  }
</script>

<div class="container">
  <div class="card">
    {#if success}
      <div class="success-view">
        <div class="success-icon">✓</div>
        <h2>Import successful</h2>
        <p>Your backup has been restored. You can now access your account.</p>
        <div class="success-actions">
          <a href="/reeds" class="btn btn-primary">Go to reeds</a>
          <a href="/profile" class="btn btn-secondary">Go to profile</a>
        </div>
      </div>
    {:else}
      <h2>Import backup</h2>
      <p class="subtitle">Restore your account from an encrypted <code>.sxb.gz.gpg</code> backup file.</p>

      <div class="field">
        <label for="backup-file">Backup file</label>
        <input
          id="backup-file"
          type="file"
          accept=".gpg,.sxb.gz.gpg"
          bind:files
        />
      </div>

      <div class="field">
        <label for="import-password">Encryption password</label>
        <input
          id="import-password"
          type="password"
          bind:value={password}
          placeholder="Password used when exporting"
          autocomplete="current-password"
        />
      </div>

      {#if error}
        <div class="error-box">{error}</div>
      {/if}

      <div class="actions">
        <a href="/" class="btn btn-secondary">Cancel</a>
        <button
          class="btn btn-primary"
          on:click={handleImport}
          disabled={!canImport || importing}
        >
          {importing ? 'Importing...' : 'Import'}
        </button>
      </div>
    {/if}
  </div>
</div>

<style>
  .container {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    width: min(440px, 100%);
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  h2 {
    margin: 0;
    color: var(--fg);
    font-size: 1.4rem;
  }

  .subtitle {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  code {
    font-family: monospace;
    font-size: 0.85em;
    background: var(--input-bg);
    padding: 0.1em 0.3em;
    border-radius: 3px;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .field label {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--fg);
  }

  .field input[type="password"] {
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.9rem;
  }

  .field input[type="password"]:focus {
    outline: none;
    border-color: var(--primary);
  }

  .field input[type="file"] {
    font-size: 0.9rem;
    color: var(--fg);
  }

  .error-box {
    background: rgba(244, 67, 54, 0.08);
    border: 1px solid rgba(244, 67, 54, 0.3);
    border-radius: 6px;
    padding: 0.75rem;
    font-size: 0.9rem;
    color: var(--error);
    line-height: 1.5;
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    justify-content: flex-end;
    margin-top: 0.25rem;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.55rem 1.25rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    font-size: 0.9rem;
    text-decoration: none;
    transition: all 0.2s ease;
  }

  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:not(:disabled):hover { opacity: 0.9; }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover { background: var(--input-bg); }

  /* Success view */
  .success-view {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.75rem;
    text-align: center;
    padding: 1rem 0;
  }

  .success-icon {
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: rgba(76, 175, 80, 0.15);
    color: #4caf50;
    font-size: 1.75rem;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .success-view h2 {
    margin: 0;
  }

  .success-view p {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .success-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 0.5rem;
    flex-wrap: wrap;
    justify-content: center;
  }
</style>
