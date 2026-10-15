package share_test

import (
	"net/http"
	"testing"

	"github.com/anggasct/chartdb/server/testhelper"
)

func ownerWithDiagram(t *testing.T, env *testhelper.Env) *http.Client {
	t.Helper()
	client := env.Client()
	env.Register(t, client, testhelper.UniqueEmail("owner"), "password-123")
	return client
}

func createShare(t *testing.T, env *testhelper.Env, client *http.Client, diagramID string) (string, string) {
	t.Helper()
	status, body := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/diagram/"+diagramID+"/share", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("create share status %d body %v", status, body)
	}
	token, _ := body["token"].(string)
	shareID, _ := body["id"].(string)
	if token == "" || shareID == "" {
		t.Fatalf("share response missing token or id: %v", body)
	}
	return token, shareID
}

func TestShareRoundtrip(t *testing.T) {
	env := testhelper.Boot(t, nil)
	owner := ownerWithDiagram(t, env)
	status, body := testhelper.Do(t, owner, "POST", env.Server.URL+"/api/v1/diagram", map[string]any{"title": "Shared", "document": map[string]any{"tables": []any{}}})
	if status != http.StatusCreated {
		t.Fatalf("create diagram status %d", status)
	}
	diagramID, _ := body["id"].(string)

	token, shareID := createShare(t, env, owner, diagramID)

	guest := env.Client()
	status, body = testhelper.Do(t, guest, "GET", env.Server.URL+"/api/v1/s/"+token, nil)
	if status != http.StatusOK {
		t.Fatalf("guest read status %d body %v", status, body)
	}
	if body["title"] != "Shared" {
		t.Fatalf("guest read body %v", body)
	}

	status, _ = testhelper.Do(t, guest, "PUT", env.Server.URL+"/api/v1/diagram/"+diagramID, map[string]any{"tables": []any{}})
	if status != http.StatusUnauthorized {
		t.Fatalf("guest write status %d, want 401", status)
	}
	status, _ = testhelper.Do(t, guest, "DELETE", env.Server.URL+"/api/v1/diagram/"+diagramID, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("guest delete status %d, want 401", status)
	}

	status, _ = testhelper.Do(t, owner, "DELETE", env.Server.URL+"/api/v1/diagram/"+diagramID+"/share/"+shareID, nil)
	if status != http.StatusNoContent {
		t.Fatalf("revoke status %d, want 204", status)
	}
	revokedStatus, revokedBody := testhelper.Do(t, guest, "GET", env.Server.URL+"/api/v1/s/"+token, nil)
	if revokedStatus != http.StatusNotFound {
		t.Fatalf("revoked read status %d, want 404", revokedStatus)
	}
	randomStatus, randomBody := testhelper.Do(t, guest, "GET", env.Server.URL+"/api/v1/s/"+"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", nil)
	if randomStatus != http.StatusNotFound {
		t.Fatalf("random token status %d, want 404", randomStatus)
	}
	revokedErr, _ := revokedBody["error"].(map[string]any)
	randomErr, _ := randomBody["error"].(map[string]any)
	if revokedErr["code"] != randomErr["code"] || revokedErr["message"] != randomErr["message"] {
		t.Fatalf("revoked and unknown tokens must look identical: %v vs %v", revokedBody, randomBody)
	}

	status, body = testhelper.Do(t, owner, "GET", env.Server.URL+"/api/v1/diagram/"+diagramID+"/share", nil)
	if status != http.StatusOK {
		t.Fatalf("list shares status %d", status)
	}
	links, _ := body["shares"].([]any)
	if len(links) != 1 {
		t.Fatalf("expected 1 revoked link listed, got %v", body)
	}
}
