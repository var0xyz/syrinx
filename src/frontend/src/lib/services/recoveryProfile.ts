import { create, type MessageInitShape } from '@bufbuild/protobuf';
import { UserSchema } from '$lib/proto/identity_pb';
import type { RecoveryProfileSchema } from '$lib/proto/recovery_pb';
import type * as api from '$lib/types/api';

/** A stored identity record as recovery reports it. */
export function recoveryProfileOf(user: api.User): MessageInitShape<typeof RecoveryProfileSchema> {
  return {
    id: user.id,
    username: user.username,
    role: user.role,
    memberSince: user.memberSince,
    bio: user.bio,
    userSignature: user.userSignature,
    serverSignature: user.serverSignature,
    invite: user.invite,
  };
}

/** The identity record a recovery profile carries. */
export function userOfRecoveryProfile(profile: api.RecoveryProfile): api.User {
  return create(UserSchema, {
    id: profile.id,
    username: profile.username,
    role: profile.role,
    memberSince: profile.memberSince,
    bio: profile.bio,
    userSignature: profile.userSignature,
    serverSignature: profile.serverSignature,
    invite: profile.invite,
  });
}
