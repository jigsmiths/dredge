package config

import "testing"

func TestAPIKeyComesFromTheEnvironmentAndIsOffByDefault(t *testing.T) {
	t.Setenv("DREDGE_API_KEY", "")
	if k := Load().APIKey; k != "" {
		t.Errorf("default key = %q", k)
	}
	t.Setenv("DREDGE_API_KEY", "s3cret")
	if k := Load().APIKey; k != "s3cret" {
		t.Errorf("key = %q", k)
	}
}
