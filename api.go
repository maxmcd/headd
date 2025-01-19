package tunneld

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

var APIRoot = "https://maxm-tunneld.web.val.run"

type APIClient struct {
	c *http.Client
}

func NewAPIClient() *APIClient {
	return &APIClient{
		c: &http.Client{},
	}
}

func (c *APIClient) handleResponse(resp *http.Response, v interface{}) error {
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected http status %d: %s", resp.StatusCode, string(b))
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

type cliLoginRequest struct {
	Hostname string `json:"hostname"`
}

type CLILoginResponse struct {
	Id string `json:"id"`
}

func (c *APIClient) CLILogin() (*CLILoginResponse, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("error retrieving hostname for login: %w", err)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(cliLoginRequest{Hostname: hostname}); err != nil {
		return nil, fmt.Errorf("error encoding cli-login body: %w", err)
	}
	r, err := c.c.Post(APIRoot+"/cli/login", "application/json", &buf)
	if err != nil {
		return nil, fmt.Errorf("error making cli-login request: %w", err)
	}
	var resp CLILoginResponse
	if err := c.handleResponse(r, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
