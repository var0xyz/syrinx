/** The inputs that make a user worth keeping on this device. */
export interface ProtectionInputs {
  viewerID: string | null;
  following: { userId: string }[];
  userLists: { memberIds: string[] }[];
  vouches: { subjectUserID: string }[];
}

/**
 * Users whose locally-held records must not be evicted. Vouch subjects are
 * included; vouchers are not, since their key re-fetches as safely as it
 * was first fetched.
 */
export function buildProtectedUserIDs(inputs: ProtectionInputs): Set<string> {
  const protectedIDs = new Set<string>();
  if (inputs.viewerID) protectedIDs.add(inputs.viewerID);
  for (const { userId } of inputs.following) protectedIDs.add(userId);
  for (const userList of inputs.userLists) {
    for (const memberID of userList.memberIds) protectedIDs.add(memberID);
  }
  for (const vouch of inputs.vouches) {
    protectedIDs.add(vouch.subjectUserID);
  }
  return protectedIDs;
}
