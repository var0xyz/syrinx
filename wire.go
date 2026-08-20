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
	Fingerprint    string `json:"fingerprint"`
	PublicKeyArmor string `json:"publicKeyArmor"`
	Signature      string `json:"signature"`
	Secret         string `json:"secret"`
}

// federationCreateRequest is the POST /api/federation/invitations body.
type federationCreateRequest struct {
	Name                 string `json:"name"`
	RemotePublicKeyArmor string `json:"remotePublicKeyArmor"`
}

// federationCreateResponse is the POST /api/federation/invitations 201 body.
type federationCreateResponse struct {
	InviteID         string `json:"inviteId"`
	ConnectionString string `json:"connectionString"`
	Status           string `json:"status"`
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
	Fingerprint string `json:"fingerprint"`
	Signature   string `json:"signature"`
	Secret      string `json:"secret"`
}

// federationConnectResponse is the 200 body of POST /federation/connect/{inviteId}.
type federationConnectResponse struct {
	Status   string `json:"status"`
	ServerID string `json:"serverId"`
}

// federationAttemptRequest is the POST /api/federation/attempt body (admin
// pastes the connection string received out-of-band from the initiator's
// admin). Named "attempt", not "accept" — pasting the string only starts
// an attempt at redeeming the invitation; nothing is confirmed until the
// initiator's /connect callback verifies it.
type federationAttemptRequest struct {
	ConnectionString string `json:"connectionString"`
}

// federationAttemptResponse is the 200 body of POST /api/federation/attempt.
type federationAttemptResponse struct {
	Status   string `json:"status"`
	ServerID string `json:"serverId"`
}

// federationListItemWire is one row in GET /api/federation/invitations.
type federationListItemWire struct {
	InviteID           string  `json:"inviteId"`
	Name               string  `json:"name"`
	Status             string  `json:"status"`
	CreatedBy          string  `json:"createdBy"`
	CreatedByUsername  string  `json:"createdByUsername"`
	RemoteFingerprint  string  `json:"remoteFingerprint"`
	CreatedAt          string  `json:"createdAt"`
	AcceptedAt         *string `json:"acceptedAt,omitempty"`
	ServerID           *string `json:"serverId,omitempty"`
	ReviewedBy         *string `json:"reviewedBy,omitempty"`
	ReviewedByUsername *string `json:"reviewedByUsername,omitempty"`
	ReviewedAt         *string `json:"reviewedAt,omitempty"`
	ConnectionString   *string `json:"connectionString,omitempty"`
}

// federationServerWire is one row in GET /api/federation/servers — an
// approved peer (servers rows only exist post-approval; see
// ApproveFederationAttempt). No fingerprint field: see
// federationServerListRow's doc comment for why.
type federationServerWire struct {
	ServerID  string `json:"serverId"`
	Name      string `json:"name"`
	BaseURL   string `json:"baseUrl"`
	Connected bool   `json:"connected"`
	CreatedAt string `json:"createdAt"`
}

// federationAttemptWire is one row in GET /api/federation/attempts — a
// handshake attempt against a peer, at any stage (pending/approved/
// rejected). Permanent audit trail; never deleted. RemoteServerID/
// RemoteServerName/BaseURL/Fingerprint are the peer's own claims from its
// handshake payload. InvitationID/ServerID are set only when applicable
// (InvitationID on the initiator side; ServerID once approved).
type federationAttemptWire struct {
	AttemptID          string  `json:"attemptId"`
	RemoteServerID     string  `json:"remoteServerId"`
	RemoteServerName   string  `json:"remoteServerName"`
	BaseURL            string  `json:"baseUrl"`
	Fingerprint        string  `json:"fingerprint"`
	InvitationID       *string `json:"invitationId,omitempty"`
	ServerID           *string `json:"serverId,omitempty"`
	CreatedAt          string  `json:"createdAt"`
	Status             string  `json:"status"`
	ApprovedBy         *string `json:"approvedBy,omitempty"`
	ApprovedByUsername *string `json:"approvedByUsername,omitempty"`
	ApprovedAt         *string `json:"approvedAt,omitempty"`
	RejectedBy         *string `json:"rejectedBy,omitempty"`
	RejectedByUsername *string `json:"rejectedByUsername,omitempty"`
	RejectedAt         *string `json:"rejectedAt,omitempty"`
	RejectedReason     *string `json:"rejectedReason,omitempty"`
}

// federationListWire is the body of GET /api/federation/list — the mesh
// tab's combined view. Each item is fetched individually by id from its
// own endpoint for detail; this is just enough to render the list.
type federationListWire struct {
	Invitations []federationListItemWire `json:"invitations"`
	Attempts    []federationAttemptWire  `json:"attempts"`
	Servers     []federationServerWire   `json:"servers"`
}
