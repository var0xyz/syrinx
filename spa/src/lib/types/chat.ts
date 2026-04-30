export interface Chat {
  id: string;
  userId: string;
  confirmed: boolean;   // false = we are the recipient and haven't accepted yet
  pending?: boolean;    // true = we are the initiator waiting for recipient's approval
}

export interface ChatMessage {
  id: string;           // server-assigned ID (or clientId for optimistic display before server responds)
  clientId: string;
  chatId: string;
  authorId: string;
  content: string;
  status: 'sent' | 'delivered' | 'failed';
  createdAt: number;    // epoch ms
}

export interface PendingChatMessage {
  clientId: string;
  chatId: string;
  content: string;
  status: 'pending' | 'sending' | 'error';
}

export interface BlockedUser {
  userId: string;
}
