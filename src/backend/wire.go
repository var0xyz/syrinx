package main

// federationConnectionPayload is the plaintext JSON encrypted into the
// connection string (PGP to the remote server public key). ServerName is
// display-only metadata (the operator-configured SERVER_NAME), not part of
// the signed bytes — see identity.BuildFederationInvitationPayload.
type federationConnectionPayload struct {
	InviteID       string `json:"inviteId"`
	ServerID       string `json:"serverId"`
	ServerName     string `json:"serverName"`
	BaseURL        string `json:"baseUrl"`
	FrontendURL    string `json:"frontendUrl"`
	Fingerprint    string `json:"fingerprint"`
	PublicKeyArmor string `json:"publicKeyArmor"`
	Signature      string `json:"signature"`
	Secret         string `json:"secret"`
}

// federationConnectRequest is the POST /federation/connect/{inviteId} body
// (responder -> initiator, no session auth: secret + signature prove
// legitimacy). No responder-admin field: the initiator can't verify a
// remote user id, so it doesn't ask for or store one. ServerName is
// display-only metadata, not part of the signed bytes — see
// identity.BuildFederationConnectPayload.
type federationConnectRequest struct {
	ServerID    string `json:"serverId"`
	ServerName  string `json:"serverName"`
	BaseURL     string `json:"baseUrl"`
	FrontendURL string `json:"frontendUrl"`
	Fingerprint string `json:"fingerprint"`
	Signature   string `json:"signature"`
	Secret      string `json:"secret"`
}

// federationConnectResponse is the 200 body of POST /federation/connect/{inviteId}.
type federationConnectResponse struct {
	Status   string `json:"status"`
	ServerID string `json:"serverId"`
}

// federationUserIdentityWire is the body of GET /api/federation/users/{userID}/identity,
// the snapshot a peer resolves a local user through. User's own countersignature
// covers it, so the response needs no signature of its own.
type federationUserIdentityWire struct {
	User        *User  `json:"user"`
	ActiveKeyID string `json:"activeKeyID"`
}
