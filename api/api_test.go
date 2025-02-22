package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type Server struct {
}

var _ ServerInterface = &Server{}

func (s *Server) GetHosts(w http.ResponseWriter, r *http.Request) {
	Hosts := []Host{
		{
			Id:   "1",
			Name: "Host 1",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Hosts)
}

func (s *Server) GetHostsHostIdBuildBuildId(w http.ResponseWriter, r *http.Request, hostId string, buildId string) {
	build := Build{
		Id: "1",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(build)
}

func (s *Server) PostHostsHostIdBuild(w http.ResponseWriter, r *http.Request, hostId string) {
	build := Build{
		Id: "1",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(build)
}

func TestClient(t *testing.T) {
	server := &Server{}
	handler := Handler(server)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client, err := NewClientWithResponses(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	hosts, err := client.GetHostsWithResponse(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(hosts.JSON200)

}
