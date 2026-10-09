import type * as api from '$lib/types/api';

/** The unsigned hints a profile card shows next to the signed profile. */
export type UserHints = Partial<Omit<api.UserInfo, '$typeName' | '$unknown' | 'id' | 'profileTimestamp'>>;

/** Profile card / UI view: signed profile fields plus optional unsigned info. */
export type UserView = api.User & UserHints;

export function mergeUserView(
  profile: api.User | null | undefined,
  info: api.UserInfo | null | undefined
): UserView | null {
  if (!profile) return null;
  if (!info) return { ...profile };
  const { id: _id, profileTimestamp: _ts, $typeName: _type, $unknown: _unknown, ...hints } = info;
  return { ...profile, ...hints };
}

/** True when the server's profileTimestamp is strictly newer than a cached profile. */
export function profileNeedsRefresh(
  profile: api.User | null | undefined,
  info: api.UserInfo | null | undefined
): boolean {
  if (!info?.profileTimestamp) return !profile;
  if (!profile?.serverSignature?.signedAt) return true;
  return info.profileTimestamp > profile.serverSignature.signedAt;
}
