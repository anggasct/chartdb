package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/anggasct/chartdb/server/auth"
	"github.com/anggasct/chartdb/server/session"
	"github.com/anggasct/chartdb/server/testhelper"
)

type stubProvider struct {
	token auth.IdentityToken
}

func (s stubProvider) AuthCodeURL(state, nonce, verifier string) string {
	return "https://idp.example.test/authorize?state=" + state
}

func (s stubProvider) Exchange(ctx context.Context, code, verifier string) (auth.IdentityToken, error) {
	return s.token, nil
}

func noRedirect(client *http.Client) *http.Client {
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func cookieNamed(t *testing.T, client *http.Client, target, name string) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func TestRegisterLoginLogout(t *testing.T) {
	env := testhelper.Boot(t, nil)
	client := env.Client()
	email := testhelper.UniqueEmail("member")

	status, body := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/register", map[string]string{"email": email, "password": "correct-horse-9"})
	if status != http.StatusCreated {
		t.Fatalf("register status %d body %v", status, body)
	}
	if body["id"] == nil || body["primary_email"] != strings.ToLower(email) {
		t.Fatalf("register body %v", body)
	}
	if cookieNamed(t, client, env.Server.URL, session.CookieName) == "" {
		t.Fatalf("register did not set %s cookie", session.CookieName)
	}

	status, _ = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/me", nil)
	if status != http.StatusOK {
		t.Fatalf("me after register: status %d", status)
	}

	status, _ = testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/logout", nil)
	if status != http.StatusNoContent {
		t.Fatalf("logout status %d", status)
	}
	status, _ = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/me", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("me after logout: status %d, want 401", status)
	}

	status, _ = testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/login", map[string]string{"email": email, "password": "wrong-password"})
	if status != http.StatusUnauthorized {
		t.Fatalf("login wrong password: status %d, want 401", status)
	}

	status, body = testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/login", map[string]string{"email": email, "password": "correct-horse-9"})
	if status != http.StatusOK {
		t.Fatalf("login status %d body %v", status, body)
	}
	status, _ = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/me", nil)
	if status != http.StatusOK {
		t.Fatalf("me after login: status %d", status)
	}
}

func TestLoginErrorsIndistinguishable(t *testing.T) {
	env := testhelper.Boot(t, nil)
	client := env.Client()

	_, wrongBody := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/login", map[string]string{"email": testhelper.UniqueEmail("ghost"), "password": "whatever-123"})
	_, unknownBody := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/login", map[string]string{"email": testhelper.UniqueEmail("nobody"), "password": "whatever-123"})
	wrongErr, _ := wrongBody["error"].(map[string]any)
	unknownErr, _ := unknownBody["error"].(map[string]any)
	if wrongErr["code"] != "invalid_credentials" || unknownErr["code"] != "invalid_credentials" {
		t.Fatalf("credentials must be indistinguishable: %v vs %v", wrongBody, unknownBody)
	}

	email := testhelper.UniqueEmail("dup")
	env.Register(t, env.Client(), email, "password-123")
	status, body := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/register", map[string]string{"email": email, "password": "password-123"})
	if status != http.StatusConflict {
		t.Fatalf("duplicate register: status %d body %v", status, body)
	}

	status, _ = testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/auth/register", map[string]string{"email": testhelper.UniqueEmail("weak"), "password": "short"})
	if status != http.StatusBadRequest {
		t.Fatalf("weak password: status %d, want 400", status)
	}
}

func oidcState(t *testing.T, env *testhelper.Env, client *http.Client, provider string) string {
	t.Helper()
	req, err := http.NewRequest("GET", env.Server.URL+"/api/v1/auth/login/oidc?provider="+provider, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("oidc begin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("oidc begin status %d, want 302", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	return location.Query().Get("state")
}

func oidcCallback(t *testing.T, env *testhelper.Env, client *http.Client, state string) int {
	t.Helper()
	req, err := http.NewRequest("GET", env.Server.URL+"/api/v1/auth/callback?code=stub-code&state="+state, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("oidc callback: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func meID(t *testing.T, env *testhelper.Env, client *http.Client) string {
	t.Helper()
	status, body := testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/me", nil)
	if status != http.StatusOK {
		t.Fatalf("me status %d body %v", status, body)
	}
	acc, _ := body["account"].(map[string]any)
	id, _ := acc["id"].(string)
	if id == "" {
		t.Fatalf("me missing account id: %v", body)
	}
	return id
}

func TestOIDCLinkOrCreate(t *testing.T) {
	env := testhelper.Boot(t, nil)
	sub := "sub-" + uuid.NewString()[:8]
	env.API.AddProvider("stub", stubProvider{token: auth.IdentityToken{Subject: sub, Email: "alice@example.test", EmailVerified: true, Name: "Alice"}})

	first := noRedirect(env.Client())
	if got := oidcCallback(t, env, first, oidcState(t, env, first, "stub")); got != http.StatusFound {
		t.Fatalf("callback status %d, want 302", got)
	}
	accountA := meID(t, env, first)

	second := noRedirect(env.Client())
	if got := oidcCallback(t, env, second, oidcState(t, env, second, "stub")); got != http.StatusFound {
		t.Fatalf("repeat callback status %d, want 302", got)
	}
	if accountB := meID(t, env, second); accountB != accountA {
		t.Fatalf("repeat login linked a different account: %s vs %s", accountB, accountA)
	}
}

func TestOIDCUnverifiedEmailCollision(t *testing.T) {
	env := testhelper.Boot(t, nil)
	email := testhelper.UniqueEmail("bob")
	localID := env.Register(t, env.Client(), email, "password-123")
	env.API.AddProvider("stub-unverified", stubProvider{token: auth.IdentityToken{Subject: "other-" + uuid.NewString()[:8], Email: email, EmailVerified: false}})

	guest := noRedirect(env.Client())
	if got := oidcCallback(t, env, guest, oidcState(t, env, guest, "stub-unverified")); got != http.StatusFound {
		t.Fatalf("callback status %d, want 302", got)
	}
	if separate := meID(t, env, guest); separate == localID {
		t.Fatalf("unverified email match linked the local account")
	}

	var accounts int
	if err := env.Store.DB.QueryRow(`SELECT COUNT(*) FROM account a WHERE EXISTS (SELECT 1 FROM identity i WHERE i.account_id = a.id AND i.email = $1)`, strings.ToLower(email)).Scan(&accounts); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accounts != 2 {
		t.Fatalf("unverified collision must create a separate account, found %d", accounts)
	}
}

func TestOIDCLinkWhileLoggedIn(t *testing.T) {
	env := testhelper.Boot(t, nil)
	email := testhelper.UniqueEmail("carol")
	client := env.Client()
	localID := env.Register(t, client, email, "password-123")
	env.API.AddProvider("stub-link", stubProvider{token: auth.IdentityToken{Subject: "carol-" + uuid.NewString()[:8], Email: "carol-idp@example.test", EmailVerified: true}})

	redirectClient := noRedirect(client)
	if got := oidcCallback(t, env, redirectClient, oidcState(t, env, redirectClient, "stub-link")); got != http.StatusFound {
		t.Fatalf("callback status %d, want 302", got)
	}
	if linked := meID(t, env, client); linked != localID {
		t.Fatalf("explicit connect must link the current account: %s vs %s", linked, localID)
	}
	status, body := testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/me", nil)
	if status != http.StatusOK {
		t.Fatalf("me status %d", status)
	}
	identities, _ := body["identities"].([]any)
	if len(identities) != 2 {
		t.Fatalf("linked account must hold 2 identities, got %v", body)
	}
}
