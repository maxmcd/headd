package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	server := &Server{}
	handler := HandlerWithOptions(server, StdHTTPServerOptions{
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {

		},
	})

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
