import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import type { Chat, ChatMessage, PendingChatMessage, BlockedUser } from '$lib/types/chat';

const CHATS_STORE    = 'chats';
const MESSAGES_STORE = 'chatMessages';
const PENDING_STORE  = 'pendingChatMessages';
const BLOCKED_STORE  = 'blocked';

export const chatRepository = {
  // ——— Chats ———

  async get(chatId: string): Promise<Chat | null> {
    return dbService.get<Chat>(CHATS_STORE, chatId);
  },

  async getByUserId(userId: string): Promise<Chat | null> {
    const results = await dbService.getAllByIndex<Chat>(CHATS_STORE, 'userId', userId);
    return results[0] ?? null;
  },

  async getAll(): Promise<Chat[]> {
    return dbService.getAll<Chat>(CHATS_STORE);
  },

  async put(chat: Chat): Promise<void> {
    return dbService.put<Chat>(CHATS_STORE, chat);
  },

  async delete(chatId: string): Promise<void> {
    return dbService.delete(CHATS_STORE, chatId);
  },

  async pendingCount(): Promise<number> {
    const all = await dbService.getAll<Chat>(CHATS_STORE);
    return all.filter(c => !c.confirmed).length;
  },

  // ——— Messages ———

  async getMessage(id: string): Promise<ChatMessage | null> {
    return dbService.get<ChatMessage>(MESSAGES_STORE, id);
  },

  async getMessages(chatId: string): Promise<ChatMessage[]> {
    const all = await dbService.getAllByIndex<ChatMessage>(MESSAGES_STORE, 'chatId', chatId);
    return all.sort((a, b) => a.createdAt - b.createdAt);
  },

  async putMessage(msg: ChatMessage): Promise<void> {
    return dbService.put<ChatMessage>(MESSAGES_STORE, msg);
  },

  async deleteMessage(id: string): Promise<void> {
    return dbService.delete(MESSAGES_STORE, id);
  },

  async updateMessageStatus(id: string, status: ChatMessage['status']): Promise<void> {
    const msg = await dbService.get<ChatMessage>(MESSAGES_STORE, id);
    if (!msg) return;
    await dbService.put<ChatMessage>(MESSAGES_STORE, { ...msg, status });
  },

  // ——— Pending messages ———

  async getPending(clientId: string): Promise<PendingChatMessage | null> {
    return dbService.get<PendingChatMessage>(PENDING_STORE, clientId);
  },

  async putPending(msg: PendingChatMessage): Promise<void> {
    return dbService.put<PendingChatMessage>(PENDING_STORE, msg);
  },

  async deletePending(clientId: string): Promise<void> {
    return dbService.delete(PENDING_STORE, clientId);
  },

  // ——— Blocked users ———

  async isBlocked(userId: string): Promise<boolean> {
    return !!(await dbService.get<BlockedUser>(BLOCKED_STORE, userId));
  },

  async getAllBlocked(): Promise<BlockedUser[]> {
    return dbService.getAll<BlockedUser>(BLOCKED_STORE);
  },

  async block(userId: string): Promise<void> {
    await dbService.put<BlockedUser>(BLOCKED_STORE, { userId });
    await apiService.blockUser(userId);
  },

  async unblock(userId: string): Promise<void> {
    await dbService.delete(BLOCKED_STORE, userId);
    await apiService.unblockUser(userId);
  },
};
