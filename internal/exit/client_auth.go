package exit

import (
	"context"
	"net/url"
)

// Redeem trades an invite secret for a token. The client must pin the
// invite's fingerprint, so the secret only reaches the authenticated
// server; no Authorization header is sent.
func (c *Client) Redeem(ctx context.Context, secret string) (*RedeemResponse, error) {
	var resp RedeemResponse
	if err := c.do(ctx, "POST", "/v1/redeem", RedeemRequest{Secret: secret}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateInvite(ctx context.Context, req CreateInviteRequest) (*CreateInviteResponse, error) {
	var resp CreateInviteResponse
	if err := c.do(ctx, "POST", "/v1/invites", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListInvites(ctx context.Context) ([]InviteInfo, error) {
	var v struct {
		Invites []InviteInfo `json:"invites"`
	}
	if err := c.do(ctx, "GET", "/v1/invites", nil, &v); err != nil {
		return nil, err
	}
	return v.Invites, nil
}

func (c *Client) DeleteInvite(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/invites/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListTokens(ctx context.Context) ([]TokenInfo, error) {
	var v struct {
		Tokens []TokenInfo `json:"tokens"`
	}
	if err := c.do(ctx, "GET", "/v1/tokens", nil, &v); err != nil {
		return nil, err
	}
	return v.Tokens, nil
}

// DeleteToken revokes a token; unless keepPeers, its peers go too. It
// returns how many peers were removed.
func (c *Client) DeleteToken(ctx context.Context, id string, keepPeers bool) (int, error) {
	path := "/v1/tokens/" + url.PathEscape(id)
	if keepPeers {
		path += "?keep_peers=1"
	}
	var v struct {
		PeersRemoved int `json:"peers_removed"`
	}
	if err := c.do(ctx, "DELETE", path, nil, &v); err != nil {
		return 0, err
	}
	return v.PeersRemoved, nil
}

// RevokeSelf revokes the client's own enroll token if it owns no peer. It
// reports whether the token was revoked and, if not, why.
func (c *Client) RevokeSelf(ctx context.Context) (bool, string, error) {
	var v struct {
		Revoked bool   `json:"revoked"`
		Reason  string `json:"reason"`
	}
	if err := c.do(ctx, "DELETE", "/v1/tokens/self", nil, &v); err != nil {
		return false, "", err
	}
	return v.Revoked, v.Reason, nil
}
