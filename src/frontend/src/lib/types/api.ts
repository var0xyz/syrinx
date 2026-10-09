// The wire types are the generated protobuf messages; this module names the
// ones the SPA stores and passes around, plus the few local-only records.

import type { Invite as WireInvite } from '$lib/proto/invites_pb';

export type Base = {};

export type {
  AccountRemovalCert as AccountRemoval,
  BlockCert,
  KeyRevocationCert as KeyRevocation,
  ReedRemovalCert as ReedRemoval,
  Ripple,
  ServerSignature,
  ThreadRecord,
  ThreadRemoval,
  ThreadRemovalCert,
  UserSignature,
} from '$lib/proto/common_pb';
export type {
  BlockListResponse,
  FollowListResponse,
  FollowListUser,
  PublicKey,
  ServerKeyRevocation,
  User,
  UserInfo,
  UserInvite,
  Vouch,
  VouchListResponse,
  VouchWithdrawal,
} from '$lib/proto/identity_pb';
export type {
  EchoerListResponse,
  EchoerListUser,
  LikeCert as ReedLike,
  ReceivedRipple,
  ReceivedRippleListResponse,
  ReplyListItem as ReplyMeta,
  ReplyListResponse,
  RippleListResponse,
  ThreadSignatures,
} from '$lib/proto/reed_pb';
export type {
  AccountRecoveryBootstrapResponse,
  ChallengeResponse,
  RecoveryKeyNode,
  RecoveryProfile,
  RecoveryRevocation as RecoveryKeyRevocation,
} from '$lib/proto/recovery_pb';
export type {
  FederationActionResponse,
  FederationAttempt,
  FederationAttemptStatus,
  FederationCreateResponse,
  FederationInvitation,
  FederationList,
  FederationServer,
} from '$lib/proto/federation_pb';

/** An invite as this device keeps it: the server's signed invite, the
 * secret only the creator holds, and the status last read back. */
export type Invite = WireInvite & {
  secret?: string;
  status: 'pending' | 'claimed' | 'revoked';
  claimedAt?: number;
  claimedBy?: string;
  revokedAt?: number;
};
