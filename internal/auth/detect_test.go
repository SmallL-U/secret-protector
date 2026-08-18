package auth

import (
	"errors"
	"net/http"
	"testing"
)

func TestDetectSupportedSchemes(t *testing.T) {
	tests := []struct {
		name        string
		prepare     func(*http.Request)
		queryParams []string
		headerNames []string
		expected    Credential
	}{
		{
			name: "query",
			prepare: func(request *http.Request) {
				request.URL.RawQuery = "api_key=client-secret"
			},
			queryParams: []string{"token", "api_key"},
			expected: Credential{
				Scheme:     SchemeQuery,
				Token:      "client-secret",
				QueryParam: "api_key",
			},
		},
		{
			name: "bearer",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer client-secret")
			},
			queryParams: []string{"token"},
			expected: Credential{
				Scheme: SchemeBearer,
				Token:  "client-secret",
			},
		},
		{
			name: "basic",
			prepare: func(request *http.Request) {
				request.SetBasicAuth("alice", "client-secret")
			},
			queryParams: []string{"token"},
			expected: Credential{
				Scheme:   SchemeBasic,
				Token:    "client-secret",
				Username: "alice",
			},
		},
		{
			name: "custom header",
			prepare: func(request *http.Request) {
				request.Header.Set("X-API-Key", "client-secret")
			},
			headerNames: []string{"X-API-Key"},
			expected: Credential{
				Scheme:     SchemeHeader,
				Token:      "client-secret",
				HeaderName: "X-API-Key",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, "http://proxy.test/resource", nil)
			if err != nil {
				t.Fatal(err)
			}
			test.prepare(request)

			actual, err := Detect(request, test.queryParams, test.headerNames)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if actual != test.expected {
				t.Fatalf("Detect() = %#v, want %#v", actual, test.expected)
			}
		})
	}
}

func TestDetectRejectsInvalidCredentials(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(*http.Request)
		expected error
	}{
		{
			name:     "missing",
			prepare:  func(_ *http.Request) {},
			expected: ErrMissing,
		},
		{
			name: "ambiguous header and query",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer one")
				request.URL.RawQuery = "token=two"
			},
			expected: ErrAmbiguous,
		},
		{
			name: "multiple query parameters",
			prepare: func(request *http.Request) {
				request.URL.RawQuery = "token=one&api_key=two"
			},
			expected: ErrAmbiguous,
		},
		{
			name: "authorization and custom header",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer one")
				request.Header.Set("X-API-Key", "two")
			},
			expected: ErrAmbiguous,
		},
		{
			name: "repeated custom header",
			prepare: func(request *http.Request) {
				request.Header.Add("X-API-Key", "one")
				request.Header.Add("X-API-Key", "two")
			},
			expected: ErrMalformed,
		},
		{
			name: "repeated query parameter",
			prepare: func(request *http.Request) {
				request.URL.RawQuery = "token=one&token=two"
			},
			expected: ErrMalformed,
		},
		{
			name: "unsupported header",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Digest value")
			},
			expected: ErrUnsupported,
		},
		{
			name: "empty bearer",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer ")
			},
			expected: ErrMalformed,
		},
		{
			name: "empty basic password",
			prepare: func(request *http.Request) {
				request.SetBasicAuth("alice", "")
			},
			expected: ErrMalformed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, "http://proxy.test/resource", nil)
			if err != nil {
				t.Fatal(err)
			}
			test.prepare(request)

			_, err = Detect(request, []string{"token", "api_key"}, []string{"X-API-Key"})
			if !errors.Is(err, test.expected) {
				t.Fatalf("Detect() error = %v, want %v", err, test.expected)
			}
		})
	}
}
