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
  },
  clearAllData(): void {
    if (typeof localStorage === 'undefined') return;
    Object.keys(localStorage).forEach(key => {
      localStorage.removeItem(key);
    });
  },
  getAll(): Record<string, string> {
    if (typeof localStorage === 'undefined') return {};
    const data: Record<string, string> = {};
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key) {
        const value = localStorage.getItem(key);
        if (value !== null) {
          data[key] = value;
        }
      }
    }
    return data;
  }
};

