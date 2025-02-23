package api

import (
	"encoding/json"
	"net/http"
)

type Server struct {
}

func (s *Server) GetHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]Host{
		{
			Id: "123",
		},
	})
}

func (s *Server) GetHostsHostIdBuildBuildId(w http.ResponseWriter, r *http.Request, hostId string, buildId string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Build{
		Id: "123",
	})
}

func (s *Server) PostHostsHostIdBuild(w http.ResponseWriter, r *http.Request, hostId string) {
	var buildRequest BuildRequest
	err := json.NewDecoder(r.Body).Decode(&buildRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Build{
		Id: "123",
	})
}
