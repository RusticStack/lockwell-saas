package provisioning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ScalewayVault struct {
	ProjectID, Region, AuthToken string
	HTTPClient                   *http.Client
	BaseURL                      string
}

type scalewaySecret struct {
	ID, Name, Status string
	VersionCount     int `json:"version_count"`
}

func (v ScalewayVault) Put(ctx context.Context, key string, value []byte) (string, error) {
	if len(value) == 0 {
		return "", errors.New("secret value is required")
	}
	if err := v.validate(); err != nil {
		return "", err
	}
	name := secretName(key)
	secret, err := v.find(ctx, name)
	if err != nil {
		return "", err
	}
	if secret.ID == "" {
		body := map[string]any{"project_id": v.ProjectID, "name": name, "description": "Lockwell hosted credential", "type": "opaque", "path": "/lockwell/tenants", "protected": true, "tags": []string{"lockwell", "hosted-credential"}}
		if err = v.do(ctx, http.MethodPost, v.secretsPath(), nil, body, &secret); err != nil {
			secret, retryErr := v.find(ctx, name)
			if retryErr != nil || secret.ID == "" {
				return "", err
			}
		}
	}
	if secret.Status != "" && secret.Status != "ready" {
		return "", fmt.Errorf("Scaleway secret is not ready: %s", secret.Status)
	}
	version := map[string]string{"data": base64.StdEncoding.EncodeToString(value)}
	if err = v.do(ctx, http.MethodPost, v.secretsPath()+"/"+url.PathEscape(secret.ID)+"/versions", nil, version, nil); err != nil {
		return "", err
	}
	return "scaleway://" + v.Region + "/" + secret.ID, nil
}

func (v ScalewayVault) Get(ctx context.Context, ref string) ([]byte, error) {
	if err := v.validate(); err != nil {
		return nil, err
	}
	region, id, ok := parseScalewayRef(ref)
	if !ok || region != v.Region {
		return nil, errors.New("invalid Scaleway secret reference")
	}
	var response struct {
		Data string `json:"data"`
	}
	if err := v.do(ctx, http.MethodGet, v.secretsPath()+"/"+url.PathEscape(id)+"/versions/latest_enabled/access", nil, nil, &response); err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, errors.New("Scaleway secret response contained invalid base64")
	}
	if len(decoded) == 0 {
		return nil, errors.New("Scaleway secret response was empty")
	}
	return decoded, nil
}

func (v ScalewayVault) find(ctx context.Context, name string) (scalewaySecret, error) {
	q := url.Values{"project_id": {v.ProjectID}, "name": {name}, "path": {"/lockwell/tenants"}, "scheduled_for_deletion": {"false"}, "page_size": {"2"}}
	var out struct {
		Secrets []scalewaySecret `json:"secrets"`
		Total   int              `json:"total_count"`
	}
	if err := v.do(ctx, http.MethodGet, v.secretsPath(), q, nil, &out); err != nil {
		return scalewaySecret{}, err
	}
	if out.Total > 1 || len(out.Secrets) > 1 {
		return scalewaySecret{}, errors.New("multiple Scaleway secrets matched the deterministic name")
	}
	if len(out.Secrets) == 1 {
		return out.Secrets[0], nil
	}
	return scalewaySecret{}, nil
}

func (v ScalewayVault) validate() error {
	if v.ProjectID == "" || v.Region == "" || v.AuthToken == "" {
		return errors.New("Scaleway project, region, and auth token are required")
	}
	switch v.Region {
	case "fr-par", "nl-ams", "pl-waw":
	default:
		return errors.New("unsupported Scaleway Secret Manager region")
	}
	return nil
}
func (v ScalewayVault) secretsPath() string {
	return "/secret-manager/v1beta1/regions/" + url.PathEscape(v.Region) + "/secrets"
}
func (v ScalewayVault) endpoint(path string, q url.Values) string {
	base := strings.TrimRight(v.BaseURL, "/")
	if base == "" {
		base = "https://api.scaleway.com"
	}
	if len(q) > 0 {
		return base + path + "?" + q.Encode()
	}
	return base + path
}
func (v ScalewayVault) client() *http.Client {
	if v.HTTPClient != nil {
		return v.HTTPClient
	}
	return &http.Client{Timeout: 20 * time.Second}
}
func (v ScalewayVault) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.endpoint(path, q), body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", v.AuthToken)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.client().Do(req)
	if err != nil {
		return fmt.Errorf("Scaleway Secret Manager request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Scaleway Secret Manager returned HTTP %d", resp.StatusCode)
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err = json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode Scaleway Secret Manager response: %w", err)
		}
	}
	return nil
}
func secretName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "lockwell-" + hex.EncodeToString(sum[:16])
}
func parseScalewayRef(ref string) (string, string, bool) {
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "scaleway" || u.Host == "" || strings.Trim(u.Path, "/") == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", "", false
	}
	return u.Host, strings.Trim(u.Path, "/"), true
}
