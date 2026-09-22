package httpapi

import (
	"net/http"

	"github.com/basonipresent/flash-cashback/backend/internal/cashback"
)

// handleGetCampaign backs GET /campaign (FR-13). Deliberately does not
// expose the remaining budget - see spec/design.md §7.
func (d *Deps) handleGetCampaign(w http.ResponseWriter, r *http.Request) {
	status, err := cashback.GetCampaignStatus(r.Context(), d.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get campaign status")
		return
	}

	state := "ended"
	if status.Active {
		state = "active"
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": state})
}
