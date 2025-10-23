export const localStorageService = {
  get<T>(key: string): T | null {
    if (typeof localStorage === 'undefined') return null;
    const val = localStorage.getItem(key);
    if (!val) return null;
    try { return JSON.parse(val) as T; } catch { return null; }
  },
  set<T>(key: string, value: T) {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(key, JSON.stringify(value));
  },
  remove(key: string) {
    if (typeof localStorage === 'undefined') return;
    localStorage.removeItem(key);
  }
};

