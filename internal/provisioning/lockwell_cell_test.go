package provisioning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestLockwellCellProvisionsAndVerifiesTenantQuotaBucketAndCredential(t *testing.T) {
	var mu sync.Mutex
	tenant := false
	quota := int64(0)
	bucket := false
	keys := map[string]cellKey{}
	seq := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		path := r.URL.Path
		if strings.HasPrefix(path, "/admin/") && r.Header.Get("Authorization") != "Bearer admin-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch {
		case path == "/admin/api/v1/tenants/acct_1" && r.Method == http.MethodGet:
			if !tenant {
				http.Error(w, "missing", 404)
				return
			}
			_ = json.NewEncoder(w).Encode(cellTenant{ID: "acct_1"})
		case path == "/admin/api/v1/tenants" && r.Method == http.MethodPost:
			tenant = true
			_ = json.NewEncoder(w).Encode(cellTenant{ID: "acct_1"})
		case strings.HasSuffix(path, "/quota") && r.Method == http.MethodPut:
			var body map[string]int64
			_ = json.NewDecoder(r.Body).Decode(&body)
			quota = body["bytes"]
			_ = json.NewEncoder(w).Encode(cellQuota{TenantID: "acct_1", QuotaBytes: &quota})
		case strings.HasSuffix(path, "/quota") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(cellQuota{TenantID: "acct_1", QuotaBytes: &quota})
		case strings.HasSuffix(path, "/keys") && r.Method == http.MethodGet:
			list := []cellKey{}
			for _, key := range keys {
				list = append(list, key)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": list})
		case strings.HasSuffix(path, "/keys") && r.Method == http.MethodPost:
			seq++
			id := "key-" + string(rune('0'+seq))
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			key := cellKey{ID: id, AccessKeyID: "AKIA" + id, SecretKey: "secret" + id, Status: "active"}
			keys[id] = key
			_ = json.NewEncoder(w).Encode(key)
		case strings.HasSuffix(path, "/revoke") && r.Method == http.MethodPost:
			parts := strings.Split(path, "/")
			id := parts[len(parts)-2]
			key := keys[id]
			key.Status = "revoked"
			key.SecretKey = ""
			keys[id] = key
			_ = json.NewEncoder(w).Encode(key)
		case path == "/api/v1/auth/token" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "native-token", "expiresIn": 300})
		case path == "/api/v1/buckets" && r.Method == http.MethodPost:
			if r.Header.Get("Authorization") != "Bearer native-token" {
				http.Error(w, "unauthorized", 401)
				return
			}
			bucket = true
			_ = json.NewEncoder(w).Encode(cellBucket{Name: "data"})
		case path == "/api/v1/buckets/data" && r.Method == http.MethodGet:
			if !bucket {
				http.Error(w, "missing", 404)
				return
			}
			_ = json.NewEncoder(w).Encode(cellBucket{Name: "data"})
		default:
			http.Error(w, "unexpected "+r.Method+" "+path, 404)
		}
	}))
	defer server.Close()
	vault := &memoryVault{values: map[string][]byte{}}
	r := Reservation{ID: "p1", TenantID: "acct_1", BucketName: "data", QuotaBytes: 1 << 30, AdminEndpoint: server.URL, PublicEndpoint: server.URL}
	result, err := (LockwellCell{HTTPClient: server.Client()}).Provision(context.Background(), r, "admin-token", vault)
	if err != nil {
		t.Fatal(err)
	}
	if result.AccessKeyID == "" || result.SecretRef == "" {
		t.Fatalf("result=%#v", result)
	}
	value, err := vault.Get(context.Background(), result.SecretRef)
	if err != nil || !strings.HasPrefix(string(value), "secret") {
		t.Fatalf("vault value=%q err=%v", value, err)
	}
	mu.Lock()
	active := 0
	revoked := 0
	for _, key := range keys {
		if key.Status == "active" {
			active++
		} else {
			revoked++
		}
	}
	if active != 1 || revoked != 1 {
		t.Fatalf("active=%d revoked=%d", active, revoked)
	}
	mu.Unlock()
	if err := (LockwellCell{HTTPClient: server.Client()}).Suspend(context.Background(), r, "admin-token", result.AccessKeyID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, key := range keys {
		if key.Status == "active" {
			t.Fatalf("key %s remained active", key.ID)
		}
	}
}
