package httpapi

import (
	"errors"
	"net/http"

	"mihomo-manager/internal/manager"
)

func (r *Router) handleTraffic(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	group := req.URL.Query().Get("groupBy")
	if group != "" && group != "user" && group != "ip" {
		http.Error(w, "invalid groupBy", http.StatusBadRequest)
		return
	}
	report, err := r.service.TrafficReport(req.URL.Query().Get("period"), group != "ip")
	if errors.Is(err, manager.ErrTrafficPeriod) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
