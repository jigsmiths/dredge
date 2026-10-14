package config

import "testing"

func TestPyroscopeIsOffUnlessAnAddressIsSet(t *testing.T) {
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", "")
	if a := Load().PyroscopeServerAddress; a != "" {
		t.Errorf("default address = %q", a)
	}
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", "http://pyroscope:4040")
	if a := Load().PyroscopeServerAddress; a != "http://pyroscope:4040" {
		t.Errorf("address = %q", a)
	}
}
