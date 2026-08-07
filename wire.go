package main

// federationConnectionPayload is the plaintext JSON encrypted into the
// connection string (PGP to the remote server public key).
type federationConnectionPayload struct {
	InviteID        string `json:"inviteId"`
	ServerID        string `json:"serverId"`
	BaseURL         string `json:"baseUrl"`
	Fingerprint     string `json:"fingerprint"`
	PublicKeyArmor  string `json:"publicKeyArmor"`
	Signature       string `json:"signature"`
	Secret          string `json:"secret"`
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

// federationListItemWire is one row in GET /api/federation/invitations.
type federationListItemWire struct {
	InviteID          string  `json:"inviteId"`
	Name              string  `json:"name"`
	Status            string  `json:"status"`
	CreatedBy         string  `json:"createdBy"`
	CreatedByUsername string  `json:"createdByUsername"`
	RemoteFingerprint string  `json:"remoteFingerprint"`
	CreatedAt         string  `json:"createdAt"`
	AcceptedAt          *string `json:"acceptedAt,omitempty"`
	ApprovedAt          *string `json:"approvedAt,omitempty"`
	ReviewedBy          *string `json:"reviewedBy,omitempty"`
	ReviewedByUsername  *string `json:"reviewedByUsername,omitempty"`
	ReviewedAt          *string `json:"reviewedAt,omitempty"`
	ConnectionString    *string `json:"connectionString,omitempty"`
}
