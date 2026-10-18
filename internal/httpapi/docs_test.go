package httpapi

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

func TestDocumentationRoutes(t *testing.T) {
	_, handler, _ := testHandler(t)
	for _, tc := range []struct {
		path        string
		contentType string
		contains    string
		status      int
	}{
		{"/docs", "text/html", "", http.StatusTemporaryRedirect},
		{"/docs/", "text/html", "./swagger-initializer.js", http.StatusOK},
		{"/docs/swagger-initializer.js", "javascript", "../api/openapi.yaml", http.StatusOK},
		{"/docs/swagger-ui-bundle.js", "javascript", "SwaggerUIBundle", http.StatusOK},
		{"/docs/swagger-ui.css", "text/css", ".swagger-ui", http.StatusOK},
		{"/api/openapi.yaml", "application/yaml", "openapi: 3.0.3", http.StatusOK},
		{"/docs/missing.js", "text/plain", "404", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := request(handler, http.MethodGet, tc.path, "", "")
			if response.Code != tc.status || !strings.Contains(response.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(response.Body.String(), tc.contains) {
				t.Fatalf("documentation response: status=%d content-type=%q", response.Code, response.Header().Get("Content-Type"))
			}
			if tc.status == http.StatusTemporaryRedirect && response.Header().Get("Location") != "/docs/" {
				t.Fatalf("unexpected documentation redirect: %q", response.Header().Get("Location"))
			}
		})
	}
}

func TestOpenAPICoversRoutes(t *testing.T) {
	_, handler, _ := testHandler(t)
	response := request(handler, http.MethodGet, "/api/openapi.yaml", "", "")
	var schema struct {
		OpenAPI  string                `yaml:"openapi"`
		Security []map[string][]string `yaml:"security"`
		Paths    map[string]map[string]struct {
			OperationID string                 `yaml:"operationId"`
			Summary     string                 `yaml:"summary"`
			Description string                 `yaml:"description"`
			Security    *[]map[string][]string `yaml:"security"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(response.Body.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.OpenAPI != "3.0.3" || !reflect.DeepEqual(schema.Security, []map[string][]string{{"SessionToken": {}}}) {
		t.Fatalf("unexpected OpenAPI version or default security: %q %#v", schema.OpenAPI, schema.Security)
	}
	documented := make(map[string]bool)
	operationIDs := make(map[string]bool)
	for path, methods := range schema.Paths {
		for method, operation := range methods {
			key := strings.ToUpper(method) + " " + path
			documented[key] = true
			if operation.OperationID == "" || operationIDs[operation.OperationID] || operation.Summary == "" || operation.Description == "" {
				t.Fatalf("missing description or duplicate operation ID: %s", key)
			}
			operationIDs[operation.OperationID] = true
			public := key == "GET /healthz" || key == "POST /hello"
			if public {
				if operation.Security == nil || len(*operation.Security) != 0 {
					t.Fatalf("public endpoint requires a token in OpenAPI: %s", key)
				}
			} else if operation.Security != nil {
				t.Fatalf("protected endpoint overrides default Bearer authentication: %s", key)
			}
		}
	}
	registered := make(map[string]bool)
	err := chi.Walk(handler.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if route != "/docs" && route != "/docs/*" && route != "/api/openapi.yaml" {
			registered[method+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(documented, registered) {
		t.Fatalf("OpenAPI routes differ from chi: documented=%v registered=%v", documented, registered)
	}
}
