package web

import (
	"io/fs"
	"mime"
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

func TestBundledFontTypesDoNotDependOnHostRegistry(t *testing.T) {
	for ext, want := range map[string]string{".woff2": "font/woff2", ".woff": "font/woff"} {
		if got := mime.TypeByExtension(ext); got != want {
			t.Fatalf("%s served as %q, want %q", ext, got, want)
		}
	}
}
