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

