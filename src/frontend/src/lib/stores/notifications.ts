import { writable } from 'svelte/store';

export interface Notification {
  id: string;
  type: 'error' | 'warning' | 'info' | 'success';
  message: string;
  duration?: number;
  timer?: number;
  paused?: boolean;
}

function createNotificationStore() {
  const { subscribe, set, update } = writable<Notification[]>([]);
  let notifications: Notification[] = [];
  let dismissCallbacks: ((id: string) => void)[] = [];

  subscribe((value) => {
    notifications = value;
  });

  function add(notification: Omit<Notification, 'id' | 'timer' | 'paused'>): string {
    const id = Math.random().toString(36).substr(2, 9);
    const duration = notification.duration || 5000;

    const newNotification: Notification = {
      ...notification,
      id,
      duration,
      paused: false
    };

    // Auto-hide for info and success notifications
    if (notification.type === 'info' || notification.type === 'success') {
      newNotification.timer = window.setTimeout(() => {
        dismiss(id);
      }, duration);
    }

    update((current) => [...current, newNotification]);
    return id;
  }

  function dismiss(id: string) {
    update((current) => {
      const notification = current.find(n => n.id === id);
      if (notification && notification.timer) {
        clearTimeout(notification.timer);
      }
      return current.filter(n => n.id !== id);
    });
  }

  function dismissWithAnimation(id: string) {
    // Notify callbacks about dismiss to trigger animation
    dismissCallbacks.forEach(callback => callback(id));

    // Wait for animation to complete before actually removing
    setTimeout(() => {
      dismiss(id);
    }, 300);
  }

  function onDismiss(callback: (id: string) => void) {
    dismissCallbacks.push(callback);
    return () => {
      const index = dismissCallbacks.indexOf(callback);
      if (index > -1) {
        dismissCallbacks.splice(index, 1);
      }
    };
  }

  function pause(id: string) {
    update((current) => {
      return current.map(notification => {
        if (notification.id === id && notification.timer && !notification.paused) {
          clearTimeout(notification.timer);
          return { ...notification, paused: true };
        }
        return notification;
      });
    });
  }

  function resume(id: string) {
    update((current) => {
      return current.map(notification => {
        if (notification.id === id && notification.paused && notification.duration) {
          const remainingTime = notification.duration;
          notification.timer = window.setTimeout(() => {
            dismiss(id);
          }, remainingTime);
          return { ...notification, paused: false };
        }
        return notification;
      });
    });
  }

  function clear() {
    notifications.forEach(notification => {
      if (notification.timer) {
        clearTimeout(notification.timer);
      }
    });
    set([]);
  }

  // Convenience methods
  function error(message: string) {
    add({ type: 'error', message });
  }

  function warning(message: string) {
    add({ type: 'warning', message });
  }

  function info(message: string, duration?: number): string {
    return add({ type: 'info', message, duration });
  }

  function success(message: string, duration?: number) {
    add({ type: 'success', message, duration });
  }

  return {
    subscribe,
    add,
    dismiss,
    dismissWithAnimation,
    pause,
    resume,
    clear,
    onDismiss,
    error,
    warning,
    info,
    success
  };
}

export const notificationStore = createNotificationStore();
