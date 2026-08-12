package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ScalewayMailer struct {
	ProjectID, Region, AuthToken, FromEmail, FromName, VerifyURL, BaseURL string
	HTTPClient                                                            *http.Client
}

func (m ScalewayMailer) SendVerification(ctx context.Context, email, token string) error {
	if m.ProjectID == "" || m.Region != "fr-par" || m.AuthToken == "" || m.FromEmail == "" || m.VerifyURL == "" {
		return errors.New("Scaleway Transactional Email is not completely configured")
	}
	target, err := url.Parse(m.VerifyURL)
	if err != nil || target.Scheme != "https" || target.Host == "" {
		return errors.New("verification URL must be an absolute HTTPS URL")
	}
	q := target.Query()
	q.Set("token", token)
	target.RawQuery = q.Encode()
	text := "Verify your Lockwell hosted account: " + target.String() + "\n\nThis link expires in one hour. If you did not create the account, ignore this message."
	payload := map[string]any{"project_id": m.ProjectID, "from": map[string]string{"email": m.FromEmail, "name": m.FromName}, "to": []map[string]string{{"email": email}}, "subject": "Verify your Lockwell account", "text": text}
	raw, _ := json.Marshal(payload)
	base := strings.TrimRight(m.BaseURL, "/")
	if base == "" {
		base = "https://api.scaleway.com"
	}
	endpoint := base + "/transactional-email/v1alpha1/regions/" + url.PathEscape(m.Region) + "/emails"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", m.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	client := m.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Scaleway Transactional Email returned HTTP %d", resp.StatusCode)
	}
	return nil
}
