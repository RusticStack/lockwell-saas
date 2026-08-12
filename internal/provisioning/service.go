package provisioning

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNoCapacity        = errors.New("no hosted cell capacity")
	ErrNotReady          = errors.New("tenant provision is not ready")
	ErrInvalidRedemption = errors.New("invalid or expired credential redemption")
)

type Reservation struct {
	ID, AccountID, CellID, Region, PublicEndpoint, AdminEndpoint, AdminSecretRef, TenantID, BucketName string
	Status                                                                                             string
}

type Credential struct {
	Endpoint, Region, TenantID, BucketName, AccessKeyID, SecretKey string
}

type CreatedCredential struct{ AccessKeyID, SecretKey string }

type Repository interface {
	Reserve(context.Context, string, string, time.Time) (Reservation, bool, error)
	Complete(context.Context, string, string, string, time.Time) error
	Fail(context.Context, string, string, time.Time) error
	CreateRedemption(context.Context, string, [32]byte, time.Time, time.Time) error
	ClaimRedemption(context.Context, string, [32]byte, time.Time, time.Time) (string, Reservation, string, string, error)
	CompleteRedemption(context.Context, string, time.Time) error
	ReleaseRedemption(context.Context, string) error
}

type CellProvisioner interface {
	Provision(context.Context, Reservation, string) (CreatedCredential, error)
}

type SecretVault interface {
	Put(context.Context, string, []byte) (string, error)
	Get(context.Context, string) ([]byte, error)
}

type Service struct {
	Repo          Repository
	Cells         CellProvisioner
	Vault         SecretVault
	Now           func() time.Time
	RedemptionTTL time.Duration
}

func (s Service) Provision(ctx context.Context, accountID, planCode string) (string, error) {
	now := s.now()
	r, ready, err := s.Repo.Reserve(ctx, accountID, planCode, now)
	if err != nil {
		return "", err
	}
	if !ready {
		adminToken, err := s.Vault.Get(ctx, r.AdminSecretRef)
		if err != nil {
			return "", fmt.Errorf("load cell credential: %w", err)
		}
		created, err := s.Cells.Provision(ctx, r, string(adminToken))
		if err != nil {
			_ = s.Repo.Fail(ctx, r.ID, "cell provisioning failed", now)
			return "", err
		}
		secretRef, err := s.Vault.Put(ctx, "tenant/"+r.ID, []byte(created.SecretKey))
		if err != nil {
			_ = s.Repo.Fail(ctx, r.ID, "credential vault write failed", now)
			return "", err
		}
		if err := s.Repo.Complete(ctx, r.ID, created.AccessKeyID, secretRef, now); err != nil {
			return "", err
		}
	}
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.Repo.CreateRedemption(ctx, r.ID, hash, now.Add(s.ttl()), now); err != nil {
		return "", err
	}
	return token, nil
}

func (s Service) Redeem(ctx context.Context, accountID, token string) (Credential, error) {
	if token == "" {
		return Credential{}, ErrInvalidRedemption
	}
	now := s.now()
	hash := sha256.Sum256([]byte(token))
	claimID, r, accessKeyID, secretRef, err := s.Repo.ClaimRedemption(ctx, accountID, hash, now, now.Add(2*time.Minute))
	if err != nil {
		return Credential{}, ErrInvalidRedemption
	}
	secret, err := s.Vault.Get(ctx, secretRef)
	if err != nil {
		_ = s.Repo.ReleaseRedemption(ctx, claimID)
		return Credential{}, err
	}
	if err := s.Repo.CompleteRedemption(ctx, claimID, now); err != nil {
		return Credential{}, err
	}
	return Credential{Endpoint: r.PublicEndpoint, Region: r.Region, TenantID: r.TenantID, BucketName: r.BucketName, AccessKeyID: accessKeyID, SecretKey: string(secret)}, nil
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Service) ttl() time.Duration {
	if s.RedemptionTTL > 0 {
		return s.RedemptionTTL
	}
	return 15 * time.Minute
}

func newToken() (string, [32]byte, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", [32]byte{}, err
	}
	t := base64.RawURLEncoding.EncodeToString(b[:])
	return t, sha256.Sum256([]byte(t)), nil
}
