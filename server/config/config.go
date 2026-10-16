package config

import (
	"os"
	"strconv"
	"time"
)

const (
	DefaultAddr              = ":8080"
	DefaultSessionLifetime   = 30 * 24 * time.Hour
	DefaultMaxDocBytes       = 32 << 20
	DefaultLoginPerMinute    = 10
	DefaultRegisterPerHour   = 5
	DefaultArgonMemory       = 64 * 1024
	DefaultArgonTime         = 3
	DefaultArgonThreads      = 2
	DefaultMaxWSMsgBytes     = 1 << 20
	DefaultMaxConnPerDiagram = 50
	DefaultSnapshotEveryN    = 500
	DefaultAwarenessTimeout  = 30 * time.Second
)

type OIDCProvider struct {
	ID           string
	DisplayName  string
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type Config struct {
	Addr              string
	DatabaseURL       string
	PublicURL         string
	AppURL            string
	StaticDir         string
	CookieSecure      bool
	TrustProxyHeaders bool
	SessionLifetime   time.Duration
	MaxDocBytes       int64
	LoginPerMinute    int
	RegisterPerHour   int
	ArgonMemory       uint32
	ArgonTime         uint32
	ArgonThreads      uint8
	MaxWSMsgBytes     int64
	MaxConnPerDiagram int
	SnapshotEveryN    int
	AwarenessTimeout  time.Duration
	OIDC              *OIDCProvider
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getint(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func getduration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

func Load() Config {
	publicURL := getenv("CHARTDB_PUBLIC_URL", "http://localhost:8080")
	oidcIssuer := os.Getenv("CHARTDB_OIDC_ISSUER")

	cfg := Config{
		Addr:              getenv("CHARTDB_ADDR", DefaultAddr),
		DatabaseURL:       os.Getenv("CHARTDB_DATABASE_URL"),
		PublicURL:         publicURL,
		AppURL:            getenv("CHARTDB_APP_URL", publicURL),
		StaticDir:         os.Getenv("CHARTDB_STATIC_DIR"),
		CookieSecure:      getenv("CHARTDB_COOKIE_SECURE", "1") != "0",
		TrustProxyHeaders: getenv("CHARTDB_TRUST_PROXY_HEADERS", "0") == "1",
		SessionLifetime:   getduration("CHARTDB_SESSION_LIFETIME", DefaultSessionLifetime),
		MaxDocBytes:       int64(getint("CHARTDB_MAX_DOC_BYTES", DefaultMaxDocBytes)),
		LoginPerMinute:    getint("CHARTDB_LOGIN_RATE_PER_MIN", DefaultLoginPerMinute),
		RegisterPerHour:   getint("CHARTDB_REGISTER_RATE_PER_HOUR", DefaultRegisterPerHour),
		ArgonMemory:       uint32(getint("CHARTDB_ARGON_MEMORY_KIB", DefaultArgonMemory)),
		ArgonTime:         uint32(getint("CHARTDB_ARGON_TIME", DefaultArgonTime)),
		ArgonThreads:      uint8(getint("CHARTDB_ARGON_THREADS", DefaultArgonThreads)),
		MaxWSMsgBytes:     int64(getint("CHARTDB_MAX_WS_MSG_BYTES", DefaultMaxWSMsgBytes)),
		MaxConnPerDiagram: getint("CHARTDB_MAX_CONN_PER_DIAGRAM", DefaultMaxConnPerDiagram),
		SnapshotEveryN:    getint("CHARTDB_SNAPSHOT_EVERY_N_UPDATES", DefaultSnapshotEveryN),
		AwarenessTimeout:  getduration("CHARTDB_AWARENESS_TIMEOUT", DefaultAwarenessTimeout),
	}

	if oidcIssuer != "" {
		id := getenv("CHARTDB_OIDC_ID", "oidc")
		cfg.OIDC = &OIDCProvider{
			ID:           id,
			DisplayName:  getenv("CHARTDB_OIDC_DISPLAY_NAME", id),
			Issuer:       oidcIssuer,
			ClientID:     os.Getenv("CHARTDB_OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("CHARTDB_OIDC_CLIENT_SECRET"),
			RedirectURL:  getenv("CHARTDB_OIDC_REDIRECT_URL", publicURL+"/api/v1/auth/callback"),
		}
	}

	return cfg
}
