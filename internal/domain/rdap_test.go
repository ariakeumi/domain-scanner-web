package domain

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRDAPAvailable(t *testing.T) {
	const domain = "example.li"

	cases := []struct {
		name    string
		status  int
		body    string
		want    bool
		wantErr bool
	}{
		{
			name:   "404 means unregistered",
			status: http.StatusNotFound,
			body:   `{"errorCode": 404}`,
			want:   true,
		},
		{
			name:   "200 with matching ldhName means registered",
			status: http.StatusOK,
			body:   `{"ldhName": "EXAMPLE.LI", "handle": "abc"}`,
			want:   false,
		},
		{
			name:   "200 with only handle still means registered",
			status: http.StatusOK,
			body:   `{"handle": "abc"}`,
			want:   false,
		},
		{
			name:    "200 with unrelated body is an error",
			status:  http.StatusOK,
			body:    `{"notices": ["whois blocked"]}`,
			wantErr: true,
		},
		{
			name:    "429 rate limit is an error",
			status:  http.StatusTooManyRequests,
			body:    `{"errorCode": 429}`,
			wantErr: true,
		},
		{
			name:    "500 is an error",
			status:  http.StatusInternalServerError,
			body:    `{"errorCode": 500}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/rdap+json" {
					t.Errorf("missing RDAP Accept header, got %q", r.Header.Get("Accept"))
				}
				w.Header().Set("Content-Type", "application/rdap+json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()

			// Point the .li entry at the test server for the duration.
			original := rdapBaseURLs["li"]
			rdapBaseURLs["li"] = ts.URL + "/domain/"
			defer func() { rdapBaseURLs["li"] = original }()

			if !hasRDAPForDomain(domain) {
				t.Fatal("hasRDAPForDomain(example.li) = false, want true")
			}

			got, err := rdapAvailable(domain)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("rdapAvailable() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("rdapAvailable() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("rdapAvailable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRDAPAvailableUnknownTLD(t *testing.T) {
	if _, err := rdapAvailable("example.com"); err == nil {
		t.Fatal("rdapAvailable(example.com) should error: no RDAP endpoint configured")
	}
	if hasRDAPForDomain("example.com") {
		t.Fatal("hasRDAPForDomain(example.com) = true, want false")
	}
}
