package api

import (
	"context"
	"encoding/json"
	"net/http"
)

type Server struct {
	api API
}

type API interface {
	GetHosts(ctx context.Context) ([]Host, error)
	GetBuild(ctx context.Context, hostId string, buildId string) (*Build, error)
	CreateBuild(ctx context.Context, hostId string, br BuildRequest) (*Build, error)
}

var _ ServerInterface = &Server{}

func (s *Server) GetHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.api.GetHosts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(hosts)
}

func (s *Server) GetHostsHostIdBuildBuildId(w http.ResponseWriter, r *http.Request, hostId string, buildId string) {
	build, err := s.api.GetBuild(r.Context(), hostId, buildId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(build)
}

func (s *Server) PostHostsHostIdBuild(w http.ResponseWriter, r *http.Request, hostId string) {
	var buildRequest BuildRequest
	err := json.NewDecoder(r.Body).Decode(&buildRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	build, err := s.api.CreateBuild(r.Context(), hostId, buildRequest)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(build)
}
