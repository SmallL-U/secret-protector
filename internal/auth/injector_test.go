package auth

import (
	"net/http"
	"testing"

	"secret-protector/internal/config"
)

func TestAutoInjectorFollowsDownstreamScheme(t *testing.T) {
	tests := []struct {
		name       string
		config     config.UpstreamAuth
		downstream Credential
		assert     func(*testing.T, *http.Request)
	}{
		{
			name:   "bearer",
			config: config.UpstreamAuth{Mode: "auto", Token: "upstream-token"},
			downstream: Credential{
				Scheme: SchemeBearer,
			},
			assert: func(t *testing.T, request *http.Request) {
				if actual := request.Header.Get("Authorization"); actual != "Bearer upstream-token" {
					t.Fatalf("Authorization = %q", actual)
				}
			},
		},
		{
			name:   "query follows parameter name",
			config: config.UpstreamAuth{Mode: "auto", Token: "upstream-token"},
			downstream: Credential{
				Scheme:     SchemeQuery,
				QueryParam: "api_key",
			},
			assert: func(t *testing.T, request *http.Request) {
				if actual := request.URL.Query().Get("api_key"); actual != "upstream-token" {
					t.Fatalf("api_key = %q", actual)
				}
			},
		},
		{
			name: "basic uses configured overrides",
			config: config.UpstreamAuth{
				Mode:     "auto",
				Token:    "upstream-token",
				Username: "service-user",
				Password: "service-password",
			},
			downstream: Credential{
				Scheme:   SchemeBasic,
				Username: "client-user",
			},
			assert: func(t *testing.T, request *http.Request) {
				username, password, ok := request.BasicAuth()
				if !ok || username != "service-user" || password != "service-password" {
					t.Fatalf("BasicAuth() = %q, %q, %v", username, password, ok)
				}
			},
		},
		{
			name:   "basic falls back to downstream username and token",
			config: config.UpstreamAuth{Mode: "auto", Token: "upstream-token"},
			downstream: Credential{
				Scheme:   SchemeBasic,
				Username: "client-user",
			},
			assert: func(t *testing.T, request *http.Request) {
				username, password, ok := request.BasicAuth()
				if !ok || username != "client-user" || password != "upstream-token" {
					t.Fatalf("BasicAuth() = %q, %q, %v", username, password, ok)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			injector, err := NewInjector(test.config)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(http.MethodGet, "http://upstream.test/resource", nil)
			if err != nil {
				t.Fatal(err)
			}

			injector.Inject(request, test.downstream)
			test.assert(t, request)
		})
	}
}

func TestExplicitInjectors(t *testing.T) {
	tests := []struct {
		name   string
		config config.UpstreamAuth
		assert func(*testing.T, *http.Request)
	}{
		{
			name:   "bearer",
			config: config.UpstreamAuth{Mode: "bearer", Token: "upstream-token"},
			assert: func(t *testing.T, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer upstream-token" {
					t.Fatal("Bearer token was not injected")
				}
			},
		},
		{
			name:   "query",
			config: config.UpstreamAuth{Mode: "query", Token: "upstream-token", QueryParam: "key"},
			assert: func(t *testing.T, request *http.Request) {
				if request.URL.Query().Get("key") != "upstream-token" {
					t.Fatal("query token was not injected")
				}
			},
		},
		{
			name: "basic",
			config: config.UpstreamAuth{
				Mode:     "basic",
				Username: "service-user",
				Password: "service-password",
			},
			assert: func(t *testing.T, request *http.Request) {
				username, password, ok := request.BasicAuth()
				if !ok || username != "service-user" || password != "service-password" {
					t.Fatal("Basic credentials were not injected")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			injector, err := NewInjector(test.config)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(http.MethodGet, "http://upstream.test/resource", nil)
			if err != nil {
				t.Fatal(err)
			}

			injector.Inject(request, Credential{Scheme: SchemeBearer})
			test.assert(t, request)
		})
	}
}
