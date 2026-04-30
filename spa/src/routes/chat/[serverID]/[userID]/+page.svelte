<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';
  import { chatRepository } from '$lib/repositories/chat';
  import { userRepository } from '$lib/repositories/user';
  import { pendingChatCount } from '$lib/stores/chat';
  import type { ChatMessage, PendingChatMessage } from '$lib/types/chat';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';

  $: userId = $page.params.userID;

  let status: 'loading' | 'ready' = 'loading';
  let chat = null;
  let otherUser = null;
  let currentUserId: string | null = null;
  let messages: ChatMessage[] = [];
  let pendingMessages: PendingChatMessage[] = [];

  $: isRecipient   = chat && !chat.confirmed;
  $: isAwaiting    = chat && chat.confirmed && chat.pending;
  $: isEstablished = chat && chat.confirmed && !chat.pending;
  $: inputDisabled = !isEstablished && !(!chat);

  let messageText = '';
  let sending = false;
  let messagesEl: HTMLElement;

  onMount(() => {
    authService.getCurrentUser().then(async (currentUser) => {
      if (!currentUser) { status = 'ready'; return; }
      currentUserId = currentUser.id;

      otherUser = await userRepository.getByUserId(userId).catch(() => null);
      chat = await chatRepository.getByUserId(userId);

      if (chat) {
        if (!chat.confirmed) pendingChatCount.update(n => Math.max(0, n - 1));
        messages = await chatRepository.getMessages(chat.id);
      }

      status = 'ready';
      await tick();
      scrollToBottom();
    });

    serverConnection.on(ServerEvent.ChatRequestAccepted, onAccepted);
    serverConnection.on(ServerEvent.ChatMessage, onNewMessage);
    serverConnection.on(ServerEvent.ChatDeliveryConfirmation, onDelivered);
    serverConnection.on(ServerEvent.ChatSigVerifyFailed, onFailed);

    return () => {
      serverConnection.off(ServerEvent.ChatRequestAccepted, onAccepted);
      serverConnection.off(ServerEvent.ChatMessage, onNewMessage);
      serverConnection.off(ServerEvent.ChatDeliveryConfirmation, onDelivered);
      serverConnection.off(ServerEvent.ChatSigVerifyFailed, onFailed);
    };
  });

  async function onAccepted({ chatId }: { chatId: string }) {
    if (!chat || chat.id !== chatId) return;
    chat = { ...chat, pending: false };
  }

  async function onNewMessage({ serverId, clientId, chatId, senderId, content, createdAt }: any) {
    if (!chat || chat.id !== chatId) return;
    const msg: ChatMessage = { id: serverId, clientId, chatId, authorId: senderId, content, status: 'delivered', createdAt: new Date(createdAt).getTime() };
    messages = [...messages.filter(m => m.id !== serverId), msg].sort((a, b) => a.createdAt - b.createdAt);
    await tick();
    scrollToBottom();
  }

  async function onDelivered({ messageId }: { messageId: string }) {
    messages = messages.map(m => m.id === messageId ? { ...m, status: 'delivered' } : m);
  }

  async function onFailed({ messageId }: { messageId: string }) {
    messages = messages.map(m => m.id === messageId ? { ...m, status: 'failed' } : m);
  }

  function scrollToBottom() {
    if (messagesEl) requestAnimationFrame(() => { messagesEl.scrollTop = messagesEl.scrollHeight; });
  }

  // ——— First message (initiates the chat) ———
  async function sendFirstMessage() {
    if (!messageText.trim() || sending) return;
    sending = true;
    const content = messageText.trim();
    messageText = '';
    try {
      const { chatId } = await apiService.initiateChat(userId, content);
      const newChat = { id: chatId, userId, confirmed: true, pending: true };
      await chatRepository.put(newChat);
      const initialMsg: ChatMessage = { id: chatId, clientId: chatId, chatId, authorId: currentUserId!, content, status: 'sent', createdAt: Date.now() };
      await chatRepository.putMessage(initialMsg);
      serverConnection.notifyChatRequest(chatId, userId, content);
      chat = newChat;
      messages = [initialMsg];
      await tick();
      scrollToBottom();
    } catch (err) {
      console.error('Failed to send chat request:', err);
      messageText = content;
    } finally {
      sending = false;
    }
  }

  // ——— Regular message send ———
  async function sendMessage() {
    if (!messageText.trim() || sending || !chat) return;
    sending = true;
    const content = messageText.trim();
    const clientId = crypto.randomUUID();
    messageText = '';

    const pending: PendingChatMessage = { clientId, chatId: chat.id, content, status: 'sending' };
    await chatRepository.putPending(pending);
    pendingMessages = [...pendingMessages, pending];

    try {
      const { serverId, createdAt } = await apiService.sendChatMessage(chat.id, clientId, content);
      const msg: ChatMessage = { id: serverId, clientId, chatId: chat.id, authorId: currentUserId!, content, status: 'sent', createdAt: new Date(createdAt).getTime() };
      await chatRepository.putMessage(msg);
      await chatRepository.deletePending(clientId);
      pendingMessages = pendingMessages.filter(p => p.clientId !== clientId);
      messages = [...messages, msg];
      serverConnection.deliverChatMessage(chat.id);
      await tick();
      scrollToBottom();
    } catch (err: any) {
      const status = err?.message?.includes('429') ? 'error' : 'error';
      await chatRepository.putPending({ ...pending, status });
      pendingMessages = pendingMessages.map(p => p.clientId === clientId ? { ...p, status: 'error' } : p);
    } finally {
      sending = false;
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (!chat) sendFirstMessage();
      else sendMessage();
    }
  }

  // ——— Accept / Deny / Block ———
  async function accept() {
    try {
      await apiService.acceptChat(chat.id);
      await chatRepository.put({ ...chat, confirmed: true });
      serverConnection.notifyChatAccepted(chat.id, userId);
      chat = { ...chat, confirmed: true };
    } catch (err) {
      console.error('Failed to accept chat:', err);
    }
  }

  async function deny() {
    try {
      await apiService.denyChat(chat.id);
      await chatRepository.delete(chat.id);
      goto('/chats');
    } catch (err) {
      console.error('Failed to deny chat:', err);
    }
  }

  async function block() {
    try {
      await apiService.denyChat(chat.id);
      await chatRepository.delete(chat.id);
      await chatRepository.block(userId);
      serverConnection.notifyBlock(userId);
      goto('/chats');
    } catch (err) {
      console.error('Failed to block:', err);
    }
  }
</script>

<Auth>
  <div class="chat-container">
    <div class="chat-header">
      <a href="/chats" class="back-btn">←</a>
      {#if otherUser}
        <a class="chat-header-user" href="/profile/{userId}">
          {#if otherUser.avatarURL}
            <img src={otherUser.avatarURL} alt="" class="avatar" />
          {:else}
            <div class="avatar-placeholder">👤</div>
          {/if}
          <span class="username">{otherUser.username}</span>
        </a>
      {:else}
        <a class="username" href="/profile/{userId}">{userId}</a>
      {/if}
    </div>

    <div class="chat-main">
      {#if status === 'loading'}
        <div class="chat-body centered"><p class="muted">Loading…</p></div>
      {:else}
        <div class="messages" bind:this={messagesEl}>
          {#each messages as msg (msg.id)}
            <div class="bubble" class:outgoing={msg.authorId === currentUserId} class:incoming={msg.authorId !== currentUserId}>
              <p>{msg.content}</p>
              {#if msg.authorId === currentUserId}
                <span class="receipt" class:delivered={msg.status === 'delivered'} class:failed={msg.status === 'failed'}>
                  {msg.status === 'delivered' ? '✓✓' : msg.status === 'failed' ? '✗' : '✓'}
                </span>
              {/if}
            </div>
          {/each}
          {#each pendingMessages as p (p.clientId)}
            <div class="bubble outgoing pending-msg">
              <p>{p.content}</p>
              <span class="receipt">{p.status === 'error' ? '✗' : '…'}</span>
            </div>
          {/each}
        </div>

        {#if isRecipient}
          <div class="bottom-bar request-bar">
            <span class="request-label">Accept this conversation?</span>
            <div class="request-actions">
              <button class="btn primary" on:click={accept}>Accept</button>
              <button class="btn secondary" on:click={deny}>Deny</button>
              <button class="btn danger" on:click={block}>Block</button>
            </div>
          </div>

        {:else if isAwaiting}
          <div class="bottom-bar awaiting-bar">
            <p>Waiting for approval before you can send more messages.</p>
          </div>

        {:else}
          <div class="bottom-bar input-bar">
            <textarea
              class="msg-input"
              placeholder={!chat ? 'Send a message to start a conversation…' : 'Type a message…'}
              bind:value={messageText}
              on:keydown={onKeydown}
              rows="1"
              disabled={sending}
            ></textarea>
            <button
              class="send-btn"
              on:click={!chat ? sendFirstMessage : sendMessage}
              disabled={!messageText.trim() || sending}
            >Send</button>
          </div>
        {/if}
      {/if}
    </div>

    <BottomToolbar currentPage="chats" />
  </div>
</Auth>

<style>
  .chat-container {
    height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
    overflow: hidden;
  }

  .chat-main {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    min-height: 0;
  }

  .chat-header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.75rem 1rem;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .back-btn {
    text-decoration: none;
    color: var(--fg);
    font-size: 1.2rem;
    line-height: 1;
  }

  .chat-header-user {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    text-decoration: none;
    color: inherit;
  }

  .chat-header-user:hover .username { text-decoration: underline; }

  .avatar {
    width: 32px;
    height: 32px;
    border-radius: 50%;
    object-fit: cover;
  }

  .avatar-placeholder {
    width: 32px;
    height: 32px;
    border-radius: 50%;
    background: var(--input-bg);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1rem;
  }

  .username {
    font-weight: 600;
    color: var(--fg);
  }

  .chat-body {
    flex: 1;
  }

  .chat-body.centered {
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .messages {
    flex: 1;
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    overflow-y: auto;
    min-height: 0;
    overscroll-behavior: contain;
  }

  .bubble {
    display: flex;
    align-items: end;
    max-width: 72%;
    padding: 0.25rem 1rem;
    border-radius: 16px;
    font-size: 0.95rem;
    line-height: 1.4;
    position: relative;
  }

  .bubble p { margin: 0; }

  .bubble.outgoing {
    align-self: flex-end;
    background: var(--primary);
    color: var(--button-text);
    border-bottom-right-radius: 4px;
  }

  .bubble.incoming {
    align-self: flex-start;
    background: var(--surface);
    border: 1px solid var(--border);
    border-bottom-left-radius: 4px;
  }

  .bubble.pending-msg {
    opacity: 0.7;
  }

  .receipt {
    font-size: 0.7rem;
    opacity: 0.5;
    float: right;
    margin: 0;
    margin-left: 0.5rem;
  }

  .receipt.failed {
    color: #ff6b6b;
  }

  .bottom-bar {
    flex-shrink: 0;
    border-top: 1px solid var(--border);
  }

  .request-bar {
    padding: 0.875rem 1rem;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.75rem;
  }

  .request-label {
    font-weight: 600;
    color: var(--fg);
  }

  .request-actions {
    display: flex;
    gap: 0.75rem;
  }

  .awaiting-bar {
    padding: 0.875rem 1rem;
    text-align: center;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .awaiting-bar p { margin: 0; }

  .input-bar {
    display: flex;
    gap: 0.5rem;
    padding: 0.75rem 1rem;
    align-items: flex-end;
  }

  .msg-input {
    flex: 1;
    resize: none;
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.95rem;
    font-family: inherit;
    max-height: 120px;
    min-width: 80%;
  }

  .msg-input:focus { outline: none; border-color: var(--primary); }

  .muted { color: var(--muted); }

  .btn {
    padding: 0.5rem 1.25rem;
    border: none;
    border-radius: 8px;
    font-weight: 600;
    cursor: pointer;
    transition: opacity 0.2s;
  }

  .btn:hover { opacity: 0.85; }
  .btn.primary { background: var(--primary); color: var(--button-text); }
  .btn.secondary { background: var(--surface); color: var(--fg); border: 1px solid var(--border); }
  .btn.danger { background: #e53e3e; color: #fff; }

  .send-btn {
    padding: 0.55rem 1rem;
    border: none;
    border-radius: 8px;
    background: var(--primary);
    color: var(--button-text);
    font-weight: 600;
    cursor: pointer;
    white-space: nowrap;
    flex-shrink: 1;
  }

  .send-btn:disabled { opacity: 0.5; cursor: default; }
</style>
