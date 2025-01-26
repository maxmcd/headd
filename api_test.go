package tunneld

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCLILogin(t *testing.T) {
	apiClient := NewAPIClient()
	resp, err := apiClient.CLILogin()
	if err != nil {
		t.Fatalf("cli-login failed: %s", err)
	}
	t.Logf("cli-login success: %s/login/%s", apiClient.apiURL, resp.Id)
}

func TestCLILoginWithMockServer(t *testing.T) {
	// Setup test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/login" {
			t.Errorf("expected path /cli/login, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test-id"}`))
	}))
	defer ts.Close()

	// Create client with test server URL
	client := NewAPIClientWithURL(ts.URL)

	// Test login
	resp, err := client.CLILogin()
	if err != nil {
		t.Fatalf("cli-login failed: %s", err)
	}

	if resp.Id != "test-id" {
		t.Errorf("expected id 'test-id', got %s", resp.Id)
	}
}
