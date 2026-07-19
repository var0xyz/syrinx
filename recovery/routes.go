package recovery

import (
	"net/http"

	"github.com/gorilla/mux"
)

// RegisterRoutes mounts recovery HTTP endpoints on api (typically the
// /api subrouter). Call only when RECOVERY_MODE is on.
func RegisterRoutes(api *mux.Router, deps Deps) {
	api.HandleFunc("/recovery/identity/claim", deps.IssueChallenge).Methods(http.MethodGet)
	api.HandleFunc("/recovery/identity/claim", deps.ClaimIdentity).Methods(http.MethodPost)
	api.HandleFunc("/recovery/identity/claim", noop).Methods(http.MethodOptions)
	api.HandleFunc("/recovery/identity", deps.ReportPeerIdentity).Methods(http.MethodPost)
	api.HandleFunc("/recovery/identity", noop).Methods(http.MethodOptions)
}
