package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMatchingDoesNotExposeSourceUnlink(t *testing.T) {
	handler := HandlerWithDependencies(Dependencies{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
		"/incoming-groups/"+uuid.NewString()+"/matching/unlink", strings.NewReader(`{}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("matching must not unlink saved sources: status=%d body=%s", response.Code, response.Body.String())
	}
}
