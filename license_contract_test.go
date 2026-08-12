package lockwellsaas

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLicensingContract(t *testing.T) {
	license := mustReadLicenseFile(t, "LICENSE")
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(license), "\r\n", "\n")))
	if got, want := fmt.Sprintf("%x", sum), "ffcca38841adb694b6f380647e15f17c446a4d1656fed51a1e2041d064c94cc8"; got != want {
		t.Fatalf("LICENSE hash = %s, want canonical %s", got, want)
	}

	checks := map[string][]string{
		"NOTICE":                 {"Required Notice: Copyright RusticStack.", "Commercial use requires a separate written"},
		"COMMERCIAL-LICENSE.md":  {"explicit written", "does not set pricing"},
		"THIRD_PARTY_NOTICES.md": {"does not replace, narrow, or relicense", "release SBOM"},
		"README.md":              {"source-available", "not OSI Open Source", "not make hosted Lockwell available"},
	}
	for path, wants := range checks {
		body := string(mustReadLicenseFile(t, path))
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %q", path, want)
			}
		}
	}
}

func mustReadLicenseFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
