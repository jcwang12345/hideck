package api

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIDocumentsIdentityAndResourceManagement(t *testing.T) {
	data, err := os.ReadFile("openapi.hideck.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	paths := openAPIMap(t, document, "paths")
	for _, path := range []string{
		"/auth/me", "/auth/logout", "/users", "/users/{id}",
		"/users/{id}/password", "/users/{id}/sessions/revoke",
		"/cards", "/cards/{id}/disable", "/cards/{id}/enable",
		"/cards/{id}/profiles/{profile_id}/actions/activate",
		"/number-pools", "/number-pools/{id}", "/number-pools/{id}/members",
		"/number-pools/{id}/users", "/phone-numbers", "/dispatch-policies",
		"/call-records", "/audit-events", "/phone/call-requests",
	} {
		if paths[path] == nil {
			t.Errorf("OpenAPI missing identity/resource path %s", path)
		}
	}
	components := openAPIMap(t, document, "components")
	schemas := openAPIMap(t, components, "schemas")
	credential := openAPIMap(t, schemas, "PasswordCredentialStatus")
	properties := openAPIMap(t, credential, "properties")
	management := openAPIMap(t, properties, "management")
	if got, want := management["enum"], []any{"config_file", "environment", "database"}; !reflect.DeepEqual(got, want) {
		t.Errorf("PasswordCredentialStatus.management enum = %#v, want %#v", got, want)
	}
}
