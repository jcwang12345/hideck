package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/identity"
)

func TestPhoneContactRESTIsolationCoversLookupGroupsImportAndExport(t *testing.T) {
	router := newOwnedPhoneContactsTestRouter(t)

	putOwnedContact(t, router, "admin-user", identity.RoleAdmin, "10086", "管理员客服")
	putOwnedContact(t, router, "member-user", identity.RoleMember, "10086", "成员客服")

	adminList := requestOwnedContacts(t, router, "admin-user", identity.RoleAdmin)
	if len(adminList.Contacts) != 1 || adminList.Contacts[0]["name"] != "管理员客服" {
		t.Fatalf("admin contacts = %+v", adminList)
	}
	memberList := requestOwnedContacts(t, router, "member-user", identity.RoleMember)
	if len(memberList.Contacts) != 1 || memberList.Contacts[0]["name"] != "成员客服" {
		t.Fatalf("member contacts = %+v", memberList)
	}

	lookup := performOwnedRequest(router, "member-user", identity.RoleMember, http.MethodGet, "/api/phone/lookup?number=10086", nil, "")
	if lookup.Code != http.StatusOK || !strings.Contains(lookup.Body.String(), `"name":"成员客服"`) {
		t.Fatalf("member lookup status=%d body=%s", lookup.Code, lookup.Body.String())
	}

	batchBody, _ := json.Marshal(map[string]any{"atomic": true, "contacts": []map[string]string{
		{"number": "10000", "name": "管理员分组", "group_key": "admin-person"},
		{"number": "10001", "name": "管理员分组", "group_key": "admin-person"},
	}})
	batch := performOwnedRequest(router, "admin-user", identity.RoleAdmin, http.MethodPost, "/api/phone/contacts/batch", batchBody, "application/json")
	if batch.Code != http.StatusOK {
		t.Fatalf("admin batch status=%d body=%s", batch.Code, batch.Body.String())
	}
	var batchResult struct {
		Contacts []struct {
			ContactID string `json:"contact_id"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(batch.Body.Bytes(), &batchResult); err != nil || len(batchResult.Contacts) != 2 {
		t.Fatalf("admin batch decode=%+v err=%v", batchResult, err)
	}
	renameBody, _ := json.Marshal(map[string]string{"contact_id": batchResult.Contacts[0].ContactID, "name": "越权改名"})
	foreignRename := performOwnedRequest(router, "member-user", identity.RoleMember, http.MethodPut, "/api/phone/contacts/group", renameBody, "application/json")
	if foreignRename.Code != http.StatusNotFound {
		t.Fatalf("foreign group update status=%d body=%s", foreignRename.Code, foreignRename.Body.String())
	}
	foreignDelete := performOwnedRequest(router, "member-user", identity.RoleMember, http.MethodDelete,
		"/api/phone/contacts/group?contact_id="+batchResult.Contacts[0].ContactID, nil, "")
	if foreignDelete.Code != http.StatusNotFound {
		t.Fatalf("foreign group delete status=%d body=%s", foreignDelete.Code, foreignDelete.Body.String())
	}
	adminGroup, err := db.GetPhoneContactForOwner(t.Context(), "admin-user", "10000")
	if err != nil || adminGroup.Name != "管理员分组" {
		t.Fatalf("admin group after foreign update = %+v err=%v", adminGroup, err)
	}

	vcf := "BEGIN:VCARD\nVERSION:3.0\nFN:仅管理员可见\nTEL:13800138000\nEND:VCARD\n"
	imported := performOwnedRequest(router, "admin-user", identity.RoleAdmin, http.MethodPost, "/api/phone/contacts/import", multipartVCF(t, vcf), "multipart/form-data; boundary=testhideck")
	if imported.Code != http.StatusOK || !strings.Contains(imported.Body.String(), `"imported":1`) {
		t.Fatalf("admin import status=%d body=%s", imported.Code, imported.Body.String())
	}
	memberExport := performOwnedRequest(router, "member-user", identity.RoleMember, http.MethodGet, "/api/phone/contacts/export", nil, "")
	if memberExport.Code != http.StatusOK || strings.Contains(memberExport.Body.String(), "13800138000") || !strings.Contains(memberExport.Body.String(), "10086") {
		t.Fatalf("member export leaked contacts: %s", memberExport.Body.String())
	}
	adminExport := performOwnedRequest(router, "admin-user", identity.RoleAdmin, http.MethodGet, "/api/phone/contacts/export", nil, "")
	if adminExport.Code != http.StatusOK || !strings.Contains(adminExport.Body.String(), "13800138000") {
		t.Fatalf("admin export missing own import: %s", adminExport.Body.String())
	}
}

func TestPhoneContactRESTRejectsMissingPrincipal(t *testing.T) {
	router := newOwnedPhoneContactsTestRouter(t)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/phone/contacts", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing principal status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newOwnedPhoneContactsTestRouter(t *testing.T) http.Handler {
	t.Helper()
	previous := db.DB
	if err := db.Init(filepath.Join(t.TempDir(), "owned-phone-contacts.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB = previous })
	gin.SetMode(gin.TestMode)
	server := &Server{}
	router := gin.New()
	api := router.Group("/api")
	api.Use(func(c *gin.Context) {
		userID := strings.TrimSpace(c.GetHeader("X-Test-User"))
		if userID != "" {
			role := identity.Role(c.GetHeader("X-Test-Role"))
			if !role.Valid() {
				role = identity.RoleMember
			}
			installPrincipal(c, identity.Principal{UserID: userID, Username: userID, Role: role}, identity.WebSession{})
		}
		c.Next()
	})
	server.registerPhoneContactRoutes(api)
	return router
}

func putOwnedContact(t *testing.T, router http.Handler, userID string, role identity.Role, number, name string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"number": number, "name": name})
	recorder := performOwnedRequest(router, userID, role, http.MethodPut, "/api/phone/contacts", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("save %s status=%d body=%s", userID, recorder.Code, recorder.Body.String())
	}
}

func requestOwnedContacts(t *testing.T, router http.Handler, userID string, role identity.Role) contactsPageResponse {
	t.Helper()
	recorder := performOwnedRequest(router, userID, role, http.MethodGet, "/api/phone/contacts", nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list %s status=%d body=%s", userID, recorder.Code, recorder.Body.String())
	}
	var result contactsPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func performOwnedRequest(router http.Handler, userID string, role identity.Role, method, target string, body []byte, contentType string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("X-Test-User", userID)
	request.Header.Set("X-Test-Role", string(role))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}
