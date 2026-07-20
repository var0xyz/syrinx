package recovery

// UserStatusResponse is the POST /api/users/status JSON body.
type UserStatusResponse struct {
	Status string `json:"status"` // complete | unknown | ongoing
}

const (
	UserStatusComplete = "complete"
	UserStatusUnknown  = "unknown"
	UserStatusOngoing  = "ongoing"
)

var (
	UserStatusCompleteResponse = UserStatusResponse{Status: UserStatusComplete}
	UserStatusUnknownResponse  = UserStatusResponse{Status: UserStatusUnknown}
	UserStatusOngoingResponse  = UserStatusResponse{Status: UserStatusOngoing}
)
