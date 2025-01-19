package tunneld

import (
	"testing"
)

func TestCLILogin(t *testing.T) {
	apiClient := NewAPIClient()
	resp, err := apiClient.CLILogin()
	if err != nil {
		t.Fatalf("cli-login failed: %s", err)
	}
	t.Logf("cli-login success: %s/login/%s", APIRoot, resp.Id)
}
