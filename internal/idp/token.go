// Package idp holds the credentials raven needs to talk to the Thunder identity server.
//
// Raven authenticates to Thunder in two different ways, and they are deliberately kept apart:
//
//   - As a service account, for reading organization units, users and groups. Those endpoints
//     require the system permission, which only an access token issued for the System resource
//     server carries. See SystemToken.
//   - With a shared secret, for checking an end user's password through the Direct API. See
//     DirectAuthSecret.
package idp

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// systemScope is the permission needed to read organization units, users and groups.
	systemScope = "system"

	// resourceSuffix completes the identifier of the System resource server that Thunder's
	// default bootstrap seeds, which is "<public url>/mcp".
	resourceSuffix = "/mcp"

	// tokenTimeout bounds a single token request.
	tokenTimeout = 10 * time.Second

	// fallbackTokenLifetime is used when Thunder omits expires_in from the response.
	fallbackTokenLifetime = time.Hour
)

// SystemToken requests a client-credentials access token for raven's service account.
//
// Raven used to obtain its privileges by driving Thunder's interactive login flow as the admin
// user. Thunder no longer permits that: only fullstack and custom applications may start a flow
// server-side, and the Console application raven used is a browser application, so the request is
// rejected with 403 "direct flow initiation is not permitted for this application type".
// Authenticating the end user with their password does not help either — the token that returns
// carries no permissions and the admin endpoints answer 403 Forbidden.
//
// So raven authenticates as what it actually is: a machine-to-machine client with a role granting
// it the system permission.
//
// The returned expiry is when the token stops being valid, not when it should be refreshed;
// callers apply their own safety margin.
func SystemToken(baseURL string) (string, time.Time, error) {
	clientID := strings.TrimSpace(os.Getenv("IDP_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("IDP_CLIENT_SECRET"))
	if clientID == "" || clientSecret == "" {
		return "", time.Time{}, fmt.Errorf("IDP_CLIENT_ID and IDP_CLIENT_SECRET must both be set")
	}

	endpoint := strings.TrimSuffix(baseURL, "/") + "/oauth2/token"
	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {systemScope},
		"resource":   {resourceIdentifier(baseURL)},
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("token request rejected: status=%d", resp.StatusCode)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", time.Time{}, fmt.Errorf("could not decode token response: %w", err)
	}

	token := strings.TrimSpace(body.AccessToken)
	if token == "" {
		return "", time.Time{}, fmt.Errorf("token response contained no access token")
	}

	lifetime := fallbackTokenLifetime
	if body.ExpiresIn > 0 {
		lifetime = time.Duration(body.ExpiresIn) * time.Second
	}

	return token, time.Now().Add(lifetime), nil
}

// resourceIdentifier names the resource server the token is requested for. Thunder derives the
// identifier from its own public URL, which is not necessarily the address raven reaches it on —
// in Kubernetes raven talks to a Service name while the identifier uses the public hostname — so
// it can be set explicitly.
func resourceIdentifier(baseURL string) string {
	if explicit := strings.TrimSpace(os.Getenv("IDP_RESOURCE")); explicit != "" {
		return explicit
	}

	return strings.TrimSuffix(baseURL, "/") + resourceSuffix
}

// httpClient matches the TLS behaviour of raven's other Thunder clients: the identity server is
// reached inside the deployment, often on a name its certificate does not cover.
func httpClient() *http.Client {
	return &http.Client{
		Timeout: tokenTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true, // #nosec G402 -- internal auth server communication, matches the other Thunder clients
			},
		},
	}
}
