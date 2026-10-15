package web

import (
	"io/fs"
	"net/http/httptest"
	"testing"
)

func TestFrontendEntryDoesNotRedirectIndexToItself(t *testing.T) {
	server := &Server{Origin: "http://localhost:8080"}
	request := httptest.NewRequest("GET", "http://localhost:8080/", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	_, err := fs.ReadFile(assets, "assets/index.html")
	expected := 200
	if err != nil {
		expected = 503
	}
	if recorder.Code != expected || recorder.Header().Get("Location") != "" {
		t.Fatalf("root must serve HTML or explicit build prerequisite, status=%d", recorder.Code)
	}
}
