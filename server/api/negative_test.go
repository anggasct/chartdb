package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/anggasct/chartdb/server/config"
	"github.com/anggasct/chartdb/server/session"
	"github.com/anggasct/chartdb/server/testhelper"
)

func TestCrossAccountForbidden(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	aliceID := env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	_ = aliceID
	status, body := testhelper.Do(t, alice, "POST", env.Server.URL+"/api/v1/diagram", map[string]any{"title": "Alice", "document": map[string]any{}})
	if status != http.StatusCreated {
		t.Fatalf("create status %d", status)
	}
	id, _ := body["id"].(string)

	bob := env.Client()
	env.Register(t, bob, testhelper.UniqueEmail("bob"), "password-123")
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		status, _ := testhelper.Do(t, bob, method, env.Server.URL+"/api/v1/diagram/"+id, map[string]any{})
		if status != http.StatusForbidden {
			t.Fatalf("%s foreign diagram: status %d, want 403", method, status)
		}
	}
}

func TestExpiredSessionUnauthorized(t *testing.T) {
	env := testhelper.Boot(t, nil)
	client := env.Client()
	accountID := env.Register(t, client, testhelper.UniqueEmail("expiring"), "password-123")
	raw := env.ExpiredSession(t, accountID)

	bare := env.Client()
	u, _ := url.Parse(env.Server.URL)
	bare.Jar.SetCookies(u, []*http.Cookie{{Name: session.CookieName, Value: raw}})
	status, _ := testhelper.Do(t, bare, "GET", env.Server.URL+"/api/v1/me", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expired session: status %d, want 401", status)
	}

	anon := env.Client()
	status, _ = testhelper.Do(t, anon, "GET", env.Server.URL+"/api/v1/me", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("missing session: status %d, want 401", status)
	}
}

func TestMalformedAndUnknownIDs(t *testing.T) {
	env := testhelper.Boot(t, nil)
	client := env.Client()
	env.Register(t, client, testhelper.UniqueEmail("ids"), "password-123")

	status, _ := testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/diagram/not-a-uuid", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("malformed id: status %d, want 400", status)
	}
	status, _ = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/diagram/"+uuid.NewString(), nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown id: status %d, want 404", status)
	}
}

func TestOversizeDocumentRejected(t *testing.T) {
	env := testhelper.Boot(t, func(cfg *config.Config) {
		cfg.MaxDocBytes = 1024
	})
	client := env.Client()
	env.Register(t, client, testhelper.UniqueEmail("big"), "password-123")

	big := strings.Repeat("x", 2048)
	status, body := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/diagram", map[string]any{"title": "Big", "document": big})
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize create: status %d body %v, want 413", status, body)
	}
}

func TestLoginRateLimited(t *testing.T) {
	env := testhelper.Boot(t, func(cfg *config.Config) {
		cfg.LoginPerMinute = 2
	})
	client := env.Client()
	payload := map[string]string{"email": testhelper.UniqueEmail("burst"), "password": "nope-1234"}
	for i := 0; i < 2; i++ {
		status, _ := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/login", payload)
		if status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i, status)
		}
	}
	status, body := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/login", payload)
	if status != http.StatusTooManyRequests {
		t.Fatalf("rate burst: status %d body %v, want 429", status, body)
	}
}

func TestRegisterRateLimited(t *testing.T) {
	env := testhelper.Boot(t, func(cfg *config.Config) {
		cfg.RegisterPerHour = 1
	})
	client := env.Client()
	status, _ := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/register", map[string]string{"email": testhelper.UniqueEmail("r1"), "password": "password-123"})
	if status != http.StatusCreated {
		t.Fatalf("first register status %d, want 201", status)
	}
	status, _ = testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/register", map[string]string{"email": testhelper.UniqueEmail("r2"), "password": "password-123"})
	if status != http.StatusTooManyRequests {
		t.Fatalf("second register status %d, want 429", status)
	}
}
