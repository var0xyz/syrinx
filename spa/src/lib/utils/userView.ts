import type * as api from '$lib/types/api';

/** Profile card / UI view: signed profile fields plus optional unsigned info. */
export type UserView = api.User & Partial<api.UserInfo>;

export function mergeUserView(
  profile: api.User | null | undefined,
  info: api.UserInfo | null | undefined
): UserView | null {
  if (!profile) return null;
  if (!info) return { ...profile };
  const { id: _id, profileTimestamp: _ts, role: _role, ...hints } = info;
  return { ...profile, ...hints };
}

/** True when the server's profileTimestamp is strictly newer than a cached profile. */
export function profileNeedsRefresh(
  profile: api.User | null | undefined,
  info: api.UserInfo | null | undefined
): boolean {
  if (!info?.profileTimestamp) return !profile;
  if (!profile?.serverSignature?.timestamp) return true;
  const cached = Date.parse(profile.serverSignature.timestamp);
  const remote = Date.parse(info.profileTimestamp);
  if (Number.isNaN(cached) || Number.isNaN(remote)) return true;
  return remote > cached;
}
