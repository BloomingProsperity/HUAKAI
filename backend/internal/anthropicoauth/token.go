package anthropicoauth

import "time"

type Token struct {
	AccessToken    string    `json:"access_token"`
	RefreshToken   string    `json:"refresh_token"`
	IDToken        string    `json:"id_token,omitempty"`
	Scope          string    `json:"scope,omitempty"`
	Email          string    `json:"email,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	AuthMode       string    `json:"auth_mode"`
	ClientID       string    `json:"client_id,omitempty"`
	ClientIDSource string    `json:"client_id_source,omitempty"`
	TokenEndpoint  string    `json:"oauth_token_endpoint,omitempty"`
}
