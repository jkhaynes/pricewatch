package dbsync

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHub(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		status   int
		wantBody string
		wantErr  string // substring; "" means success
		wantHits int
	}{
		{name: "downloads the raw file", token: "tok", status: http.StatusOK, wantBody: "SQLite format 3", wantHits: 1},
		{name: "reports an HTTP failure", token: "tok", status: http.StatusUnauthorized, wantErr: "HTTP 401", wantHits: 1},
		{name: "refuses to call without a token", token: "", wantErr: "PRICEWATCH_GITHUB_TOKEN", wantHits: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.URL.Path != "/repos/jkhaynes/pricewatch-data/contents/pricewatch.db" || r.URL.Query().Get("ref") != "db" {
					t.Errorf("requested %s", r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Accept") != "application/vnd.github.raw" {
					t.Errorf("headers = %v", r.Header)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte("SQLite format 3"))
			}))
			defer srv.Close()

			var got bytes.Buffer
			fetch := GitHub(srv.Client(), srv.URL, "jkhaynes/pricewatch-data", "db", "pricewatch.db", tt.token)
			err := fetch(t.Context(), &got)
			if hits != tt.wantHits {
				t.Errorf("%d requests, want %d", hits, tt.wantHits)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "Bearer") {
					t.Errorf("the error leaks the Authorization header: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.wantBody {
				t.Errorf("wrote %q, want %q", got.String(), tt.wantBody)
			}
		})
	}
}

func TestGitHubHonoursCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var got bytes.Buffer
	if err := GitHub(srv.Client(), srv.URL, "o/r", "db", "f.db", "tok")(ctx, &got); err == nil {
		t.Error("a cancelled context still downloaded")
	}
}
