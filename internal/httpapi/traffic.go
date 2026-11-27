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

func (r *Router) handleTrafficConnections(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	params := req.URL.Query()
	group := params.Get("groupBy")
	if group != "" && group != "user" && group != "ip" {
		http.Error(w, "invalid groupBy", http.StatusBadRequest)
		return
	}
	report, err := r.service.TrafficConnections(manager.TrafficConnectionQuery{
		Period: params.Get("period"), ByUser: group != "ip", Kind: params.Get("kind"), Subject: params.Get("subject"),
		Search: params.Get("search"), Cursor: params.Get("cursor"),
	})
	switch {
	case errors.Is(err, manager.ErrTrafficDetailQuery):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, manager.ErrTrafficPeriod):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	default:
		writeJSON(w, http.StatusOK, report)
	}
}

func (r *Router) handleTrafficAnalysis(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	params := req.URL.Query()
	group := params.Get("groupBy")
	if group != "" && group != "user" && group != "ip" {
		http.Error(w, "invalid groupBy", http.StatusBadRequest)
		return
	}
	report, err := r.service.TrafficAnalysis(manager.TrafficAnalysisQuery{
		TrafficConnectionQuery: manager.TrafficConnectionQuery{
			Period: params.Get("period"), ByUser: group != "ip", Kind: params.Get("kind"), Subject: params.Get("subject"),
		},
		Day: params.Get("day"), Hour: params.Get("hour"), Protocol: params.Get("protocol"), Target: params.Get("target"),
	})
	switch {
	case errors.Is(err, manager.ErrTrafficDetailQuery):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, manager.ErrTrafficPeriod):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, "traffic analysis unavailable", http.StatusServiceUnavailable)
	default:
		writeJSON(w, http.StatusOK, report)
	}
}
