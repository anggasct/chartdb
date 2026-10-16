package config

import (
	"testing"
)

func setEnv(t *testing.T, key, value string) {
	t.Helper()
	if value == "" {
		t.Setenv(key, "")
	} else {
		t.Setenv(key, value)
	}
}

func TestLoadAddrPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		chartAddr string
		port      string
		want      string
	}{
		{name: "explicit addr wins over port", chartAddr: ":9090", port: "8080", want: ":9090"},
		{name: "port fallback", chartAddr: "", port: "8080", want: ":8080"},
		{name: "port with colon prefix", chartAddr: "", port: ":8071", want: ":8071"},
		{name: "default when neither set", chartAddr: "", port: "", want: DefaultAddr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, "CHARTDB_ADDR", tt.chartAddr)
			setEnv(t, "PORT", tt.port)
			cfg := Load()
			if cfg.Addr != tt.want {
				t.Fatalf("Addr = %q, want %q (CHARTDB_ADDR=%q PORT=%q)", cfg.Addr, tt.want, tt.chartAddr, tt.port)
			}
		})
	}
}
