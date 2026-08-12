package provisioning

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

type LockwellCell struct {
	HTTPClient *http.Client
	UserAgent  string
}
type cellKey struct {
	ID          string `json:"id"`
	AccessKeyID string `json:"accessKeyId"`
	SecretKey   string `json:"secretKey"`
	Status      string `json:"status"`
}
type cellTenant struct {
	ID       string `json:"id"`
	Disabled bool   `json:"disabled"`
}
type cellQuota struct {
	TenantID   string `json:"tenantId"`
	QuotaBytes *int64 `json:"quotaBytes"`
}
type cellBucket struct {
	Name string `json:"name"`
}

func (c LockwellCell) Suspend(ctx context.Context, r Reservation, adminToken, accessKeyID string) error {
	if accessKeyID == "" {
		return errors.New("active access key identity is required")
	}
	keys, err := c.listKeys(ctx, r, adminToken)
	if err != nil {
		return err
	}
	var target cellKey
	for _, key := range keys {
		if key.AccessKeyID == accessKeyID {
			target = key
			break
		}
	}
	if target.ID == "" {
		return errors.New("provisioned access key was not found")
	}
	if !strings.EqualFold(target.Status, "active") {
		return nil
	}
	var ignored cellKey
	if err = c.adminJSON(ctx, r, adminToken, http.MethodPost, "/tenants/"+url.PathEscape(r.TenantID)+"/keys/"+url.PathEscape(target.ID)+"/revoke", map[string]string{"reason": "hosted entitlement suspended"}, &ignored); err != nil {
		return err
	}
	keys, err = c.listKeys(ctx, r, adminToken)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if key.ID == target.ID {
			if strings.EqualFold(key.Status, "active") {
				return errors.New("Lockwell key remained active after entitlement suspension")
			}
			return nil
		}
	}
	return errors.New("Lockwell key disappeared before suspension readback")
}

func (c LockwellCell) Provision(ctx context.Context, r Reservation, adminToken string, vault SecretVault) (ProvisionedCredential, error) {
	if r.TenantID == "" || r.BucketName == "" || r.QuotaBytes <= 0 {
		return ProvisionedCredential{}, errors.New("invalid cell reservation")
	}
	if adminToken == "" || vault == nil {
		return ProvisionedCredential{}, errors.New("cell admin token and secret vault are required")
	}
	if err := c.ensureTenant(ctx, r, adminToken); err != nil {
		return ProvisionedCredential{}, err
	}
	if err := c.ensureQuota(ctx, r, adminToken); err != nil {
		return ProvisionedCredential{}, err
	}
	if err := c.ensureBucket(ctx, r, adminToken); err != nil {
		return ProvisionedCredential{}, err
	}
	keys, err := c.listKeys(ctx, r, adminToken)
	if err != nil {
		return ProvisionedCredential{}, err
	}
	for _, key := range keys {
		if strings.EqualFold(key.Status, "active") {
			return ProvisionedCredential{}, errors.New("tenant already has an active key; reconcile prior credential delivery")
		}
	}
	var created cellKey
	scope := strings.Join([]string{"op=read:bucket=" + r.BucketName, "op=write:bucket=" + r.BucketName, "op=delete:bucket=" + r.BucketName}, ",")
	if err = c.adminJSON(ctx, r, adminToken, http.MethodPost, "/tenants/"+url.PathEscape(r.TenantID)+"/keys", map[string]string{"scopes": scope}, &created); err != nil {
		return ProvisionedCredential{}, err
	}
	if created.ID == "" || created.AccessKeyID == "" || created.SecretKey == "" {
		return ProvisionedCredential{}, errors.New("Lockwell key creation returned an incomplete credential")
	}
	revoke := func(reason string) error {
		var ignored cellKey
		if err := c.adminJSON(ctx, r, adminToken, http.MethodPost, "/tenants/"+url.PathEscape(r.TenantID)+"/keys/"+url.PathEscape(created.ID)+"/revoke", map[string]string{"reason": reason}, &ignored); err != nil {
			return err
		}
		keys, err := c.listKeys(ctx, r, adminToken)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if key.ID == created.ID {
				if strings.EqualFold(key.Status, "active") {
					return errors.New("Lockwell key remained active after revocation")
				}
				return nil
			}
		}
		return errors.New("Lockwell key disappeared before revocation readback")
	}
	ref, err := vault.Put(ctx, "tenant/"+r.ID, []byte(created.SecretKey))
	created.SecretKey = ""
	if err != nil {
		if revokeErr := revoke("hosted credential delivery failed"); revokeErr != nil {
			return ProvisionedCredential{}, errors.Join(err, revokeErr)
		}
		return ProvisionedCredential{}, err
	}
	keys, err = c.listKeys(ctx, r, adminToken)
	if err != nil {
		_ = revoke("hosted credential readback failed")
		return ProvisionedCredential{}, err
	}
	verified := false
	for _, key := range keys {
		if key.AccessKeyID == created.AccessKeyID && strings.EqualFold(key.Status, "active") {
			verified = true
		}
	}
	if !verified {
		_ = revoke("hosted credential readback failed")
		return ProvisionedCredential{}, errors.New("Lockwell credential readback did not find the active key")
	}
	return ProvisionedCredential{AccessKeyID: created.AccessKeyID, SecretRef: ref}, nil
}

func (c LockwellCell) ensureTenant(ctx context.Context, r Reservation, token string) error {
	var tenant cellTenant
	err := c.adminJSON(ctx, r, token, http.MethodGet, "/tenants/"+url.PathEscape(r.TenantID), nil, &tenant)
	if statusCode(err) == http.StatusNotFound {
		err = c.adminJSON(ctx, r, token, http.MethodPost, "/tenants", map[string]string{"id": r.TenantID, "name": r.TenantID}, &tenant)
	}
	if err != nil {
		return err
	}
	if tenant.ID != r.TenantID || tenant.Disabled {
		return errors.New("Lockwell tenant readback is not active")
	}
	return nil
}
func (c LockwellCell) ensureQuota(ctx context.Context, r Reservation, token string) error {
	var quota cellQuota
	if err := c.adminJSON(ctx, r, token, http.MethodPut, "/tenants/"+url.PathEscape(r.TenantID)+"/quota", map[string]int64{"bytes": r.QuotaBytes}, &quota); err != nil {
		return err
	}
	if err := c.adminJSON(ctx, r, token, http.MethodGet, "/tenants/"+url.PathEscape(r.TenantID)+"/quota", nil, &quota); err != nil {
		return err
	}
	if quota.TenantID != r.TenantID || quota.QuotaBytes == nil || *quota.QuotaBytes != r.QuotaBytes {
		return errors.New("Lockwell quota readback mismatch")
	}
	return nil
}
func (c LockwellCell) ensureBucket(ctx context.Context, r Reservation, token string) error {
	var key cellKey
	scope := "op=admin:bucket=" + r.BucketName + ",op=read:bucket=" + r.BucketName
	if err := c.adminJSON(ctx, r, token, http.MethodPost, "/tenants/"+url.PathEscape(r.TenantID)+"/keys", map[string]string{"scopes": scope}, &key); err != nil {
		return err
	}
	if key.ID == "" || key.AccessKeyID == "" || key.SecretKey == "" {
		return errors.New("transient bucket key was incomplete")
	}
	defer func() {
		var ignored cellKey
		_ = c.adminJSON(context.WithoutCancel(ctx), r, token, http.MethodPost, "/tenants/"+url.PathEscape(r.TenantID)+"/keys/"+url.PathEscape(key.ID)+"/revoke", map[string]string{"reason": "hosted transient bucket provisioning key"}, &ignored)
	}()
	bearer, err := c.nativeToken(ctx, r, key)
	key.SecretKey = ""
	if err != nil {
		return err
	}
	var bucket cellBucket
	err = c.publicJSON(ctx, r, bearer, http.MethodPost, "/api/v1/buckets", map[string]string{"name": r.BucketName}, &bucket)
	if statusCode(err) != http.StatusConflict && err != nil {
		return err
	}
	if err = c.publicJSON(ctx, r, bearer, http.MethodGet, "/api/v1/buckets/"+url.PathEscape(r.BucketName), nil, &bucket); err != nil {
		return err
	}
	if bucket.Name != r.BucketName {
		return errors.New("Lockwell bucket readback mismatch")
	}
	return nil
}
func (c LockwellCell) nativeToken(ctx context.Context, r Reservation, key cellKey) (string, error) {
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if err := c.publicJSON(ctx, r, "", http.MethodPost, "/api/v1/auth/token", map[string]string{"accessKeyId": key.AccessKeyID, "secretKey": key.SecretKey}, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("Lockwell native token response was empty")
	}
	return out.AccessToken, nil
}
func (c LockwellCell) listKeys(ctx context.Context, r Reservation, token string) ([]cellKey, error) {
	var out struct {
		Keys []cellKey `json:"keys"`
	}
	err := c.adminJSON(ctx, r, token, http.MethodGet, "/tenants/"+url.PathEscape(r.TenantID)+"/keys", nil, &out)
	return out.Keys, err
}

type cellHTTPError struct{ status int }

func (e cellHTTPError) Error() string { return fmt.Sprintf("Lockwell cell returned HTTP %d", e.status) }
func statusCode(err error) int {
	var target cellHTTPError
	if errors.As(err, &target) {
		return target.status
	}
	return 0
}
func (c LockwellCell) adminJSON(ctx context.Context, r Reservation, token, method, path string, in, out any) error {
	return c.do(ctx, r.AdminEndpoint, token, method, "/admin/api/v1"+path, in, out)
}
func (c LockwellCell) publicJSON(ctx context.Context, r Reservation, token, method, path string, in, out any) error {
	return c.do(ctx, r.PublicEndpoint, token, method, path, in, out)
}
func (c LockwellCell) do(ctx context.Context, endpoint, token, method, path string, in, out any) error {
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return errors.New("invalid Lockwell cell endpoint")
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	ua := c.UserAgent
	if ua == "" {
		ua = "lockwell-saas-cell/0.1"
	}
	req.Header.Set("User-Agent", ua)
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Lockwell cell request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return cellHTTPError{resp.StatusCode}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
