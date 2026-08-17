package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"secret-protector/internal/config"
)

const downstreamSecret = "client-secret"

type observedRequest struct {
	path          string
	host          string
	authorization string
	query         url.Values
	username      string
	password      string
	hasBasic      bool
}

func TestRuntimeInjectsConfiguredAuthentication(t *testing.T) {
	tests := []struct {
		name    string
		auth    config.UpstreamAuth
		prepare func(*http.Request)
		assert  func(*testing.T, observedRequest)
	}{
		{
			name: "auto follows bearer",
			auth: config.UpstreamAuth{Mode: "auto", Token: "upstream-token"},
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+downstreamSecret)
			},
			assert: func(t *testing.T, observed observedRequest) {
				if observed.authorization != "Bearer upstream-token" {
					t.Fatalf("Authorization = %q", observed.authorization)
				}
			},
		},
		{
			name: "auto follows query and parameter name",
			auth: config.UpstreamAuth{Mode: "auto", Token: "upstream-token"},
			prepare: func(request *http.Request) {
				request.URL.RawQuery = "client_key=" + downstreamSecret + "&keep=yes"
			},
			assert: func(t *testing.T, observed observedRequest) {
				if observed.query.Get("client_key") != "upstream-token" {
					t.Fatalf("client_key = %q", observed.query.Get("client_key"))
				}
				if observed.query.Get("keep") != "yes" {
					t.Fatal("unrelated query parameter was not preserved")
				}
			},
		},
		{
			name: "auto follows basic",
			auth: config.UpstreamAuth{
				Mode:     "auto",
				Token:    "upstream-token",
				Username: "service-user",
				Password: "service-password",
			},
			prepare: func(request *http.Request) {
				request.SetBasicAuth("client-user", downstreamSecret)
			},
			assert: func(t *testing.T, observed observedRequest) {
				if !observed.hasBasic || observed.username != "service-user" || observed.password != "service-password" {
					t.Fatalf("Basic auth = %q, %q, %v", observed.username, observed.password, observed.hasBasic)
				}
			},
		},
		{
			name: "explicit bearer replaces query credential",
			auth: config.UpstreamAuth{Mode: "bearer", Token: "upstream-token"},
			prepare: func(request *http.Request) {
				request.URL.RawQuery = "token=" + downstreamSecret
			},
			assert: func(t *testing.T, observed observedRequest) {
				if observed.authorization != "Bearer upstream-token" {
					t.Fatalf("Authorization = %q", observed.authorization)
				}
				if observed.query.Has("token") {
					t.Fatal("downstream query credential leaked upstream")
				}
			},
		},
		{
			name: "explicit query replaces bearer credential",
			auth: config.UpstreamAuth{Mode: "query", Token: "upstream-token", QueryParam: "api_key"},
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+downstreamSecret)
			},
			assert: func(t *testing.T, observed observedRequest) {
				if observed.authorization != "" {
					t.Fatal("downstream Authorization leaked upstream")
				}
				if observed.query.Get("api_key") != "upstream-token" {
					t.Fatalf("api_key = %q", observed.query.Get("api_key"))
				}
			},
		},
		{
			name: "explicit basic replaces bearer credential",
			auth: config.UpstreamAuth{Mode: "basic", Username: "service-user", Password: "service-password"},
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+downstreamSecret)
			},
			assert: func(t *testing.T, observed observedRequest) {
				if !observed.hasBasic || observed.username != "service-user" || observed.password != "service-password" {
					t.Fatalf("Basic auth = %q, %q, %v", observed.username, observed.password, observed.hasBasic)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observed := make(chan observedRequest, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				username, password, hasBasic := request.BasicAuth()
				observed <- observedRequest{
					path:          request.URL.Path,
					host:          request.Host,
					authorization: request.Header.Get("Authorization"),
					query:         request.URL.Query(),
					username:      username,
					password:      password,
					hasBasic:      hasBasic,
				}
				writer.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()

			cfg := proxyConfig(upstream.URL+"/base", test.auth)
			cfg.Routes[0].Upstream.StripPrefix = true
			runtime, err := NewRuntime(cfg, discardLogger())
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "http://proxy.test/api/resource", nil)
			test.prepare(request)
			response := httptest.NewRecorder()

			runtime.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			actual := <-observed
			if actual.path != "/base/resource" {
				t.Fatalf("path = %q", actual.path)
			}
			if actual.host != strings.TrimPrefix(upstream.URL, "http://") {
				t.Fatalf("host = %q", actual.host)
			}
			if actual.authorization == "Bearer "+downstreamSecret || actual.password == downstreamSecret {
				t.Fatal("downstream credential leaked upstream")
			}
			for _, values := range actual.query {
				for _, value := range values {
					if value == downstreamSecret {
						t.Fatal("downstream query credential leaked upstream")
					}
				}
			}
			test.assert(t, actual)
		})
	}
}

func TestRouterUsesLongestSegmentPrefixAndStripsIt(t *testing.T) {
	api := httptest.NewServer(echoPathHandler("api"))
	defer api.Close()
	admin := httptest.NewServer(echoPathHandler("admin"))
	defer admin.Close()

	cfg := config.New()
	cfg.Routes = []config.Route{
		testRoute("api", "/api", api.URL, true),
		testRoute("admin", "/api/admin", admin.URL, true),
	}
	runtime, err := NewRuntime(cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://proxy.test/api/admin/users?token="+downstreamSecret, nil)
	response := httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "admin:/users" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://proxy.test/apix?token="+downstreamSecret, nil)
	response = httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("segment boundary status = %d", response.Code)
	}
}

func TestRouterPreservesEscapedPathWhenStrippingPrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, request.URL.EscapedPath())
	}))
	defer upstream.Close()

	cfg := config.New()
	cfg.Routes = []config.Route{
		testRoute("api", "/api", upstream.URL+"/base", true),
	}
	runtime, err := NewRuntime(cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://proxy.test/api/items/a%2Fb?token="+downstreamSecret, nil)
	response := httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "/base/items/a%2Fb" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestRouterRejectsMissingInvalidAndAmbiguousAuthentication(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	runtime, err := NewRuntime(proxyConfig(upstream.URL, config.UpstreamAuth{Mode: "bearer", Token: "upstream-token"}), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		prepare  func(*http.Request)
		expected int
	}{
		{name: "missing", prepare: func(_ *http.Request) {}, expected: http.StatusUnauthorized},
		{
			name: "wrong token",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer wrong")
			},
			expected: http.StatusUnauthorized,
		},
		{
			name: "ambiguous",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+downstreamSecret)
				request.URL.RawQuery = "token=" + downstreamSecret
			},
			expected: http.StatusBadRequest,
		},
		{
			name: "unsupported",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Digest value")
			},
			expected: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://proxy.test/api", nil)
			test.prepare(request)
			response := httptest.NewRecorder()
			runtime.ServeHTTP(response, request)
			if response.Code != test.expected {
				t.Fatalf("status = %d, want %d", response.Code, test.expected)
			}
			if strings.Contains(response.Body.String(), downstreamSecret) {
				t.Fatal("error response exposed a token")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
}

func TestRuntimeHealthReflectsWhetherSnapshotIsAvailable(t *testing.T) {
	runtime := NewUnreadyRuntime(config.New().Server, discardLogger())
	assertHealth(t, runtime, http.MethodGet, http.StatusServiceUnavailable, `"status":"unavailable"`)
	assertHealth(t, runtime, http.MethodHead, http.StatusServiceUnavailable, "")

	request := httptest.NewRequest(http.MethodPost, "http://proxy.test/healthz", nil)
	response := httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST health status = %d", response.Code)
	}
	if actual := response.Header().Get("Allow"); actual != "GET, HEAD" {
		t.Fatalf("Allow = %q", actual)
	}

	request = httptest.NewRequest(http.MethodGet, "http://proxy.test/api", nil)
	response = httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready proxy status = %d", response.Code)
	}

	cfg := proxyConfig("https://example.test", config.UpstreamAuth{Mode: "bearer", Token: "upstream-token"})
	if err := runtime.Reload(cfg); err != nil {
		t.Fatal(err)
	}
	assertHealth(t, runtime, http.MethodGet, http.StatusOK, `"status":"ok"`)

	invalid := config.Clone(cfg)
	invalid.Routes[0].Upstream.Auth.Token = ""
	if err := runtime.Reload(invalid); err == nil {
		t.Fatal("invalid reload succeeded")
	}
	assertHealth(t, runtime, http.MethodGet, http.StatusOK, `"status":"ok"`)
}

func TestRuntimePublishesValidReloadAndKeepsLastSnapshotOnErrors(t *testing.T) {
	observed := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed <- request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	initial := proxyConfig(upstream.URL, config.UpstreamAuth{Mode: "bearer", Token: "old-upstream-token"})
	runtime, err := NewRuntime(initial, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	proxyOnce(t, runtime)
	if actual := <-observed; actual != "Bearer old-upstream-token" {
		t.Fatalf("initial Authorization = %q", actual)
	}

	next := config.Clone(initial)
	next.Routes[0].Upstream.Auth.Token = "new-upstream-token"
	if err := runtime.Reload(next); err != nil {
		t.Fatal(err)
	}
	proxyOnce(t, runtime)
	if actual := <-observed; actual != "Bearer new-upstream-token" {
		t.Fatalf("reloaded Authorization = %q", actual)
	}

	invalid := config.Clone(next)
	invalid.Routes[0].Upstream.Auth.Token = ""
	if err := runtime.Reload(invalid); err == nil {
		t.Fatal("invalid reload succeeded")
	}
	proxyOnce(t, runtime)
	if actual := <-observed; actual != "Bearer new-upstream-token" {
		t.Fatalf("Authorization after invalid reload = %q", actual)
	}

	serverChange := config.Clone(next)
	serverChange.Server.Listen = "127.0.0.1:9090"
	if err := runtime.Reload(serverChange); err == nil {
		t.Fatal("server setting reload succeeded")
	}
	proxyOnce(t, runtime)
	if actual := <-observed; actual != "Bearer new-upstream-token" {
		t.Fatalf("Authorization after server reload = %q", actual)
	}
}

func proxyConfig(upstreamURL string, authConfig config.UpstreamAuth) *config.Config {
	cfg := config.New()
	cfg.Routes = []config.Route{
		{
			Name:       "api",
			PathPrefix: "/api",
			Upstream: config.UpstreamConfig{
				URL:  upstreamURL,
				Auth: authConfig,
			},
			Downstream: config.DownstreamConfig{
				QueryParams: []string{"token", "client_key"},
				Tokens: []config.AccessToken{
					{Name: "client", Value: downstreamSecret},
				},
			},
		},
	}

	return cfg
}

func testRoute(name string, prefix string, upstreamURL string, strip bool) config.Route {
	return config.Route{
		Name:       name,
		PathPrefix: prefix,
		Upstream: config.UpstreamConfig{
			URL:         upstreamURL,
			StripPrefix: strip,
			Auth: config.UpstreamAuth{
				Mode:  "bearer",
				Token: "upstream-token",
			},
		},
		Downstream: config.DownstreamConfig{
			QueryParams: []string{"token"},
			Tokens: []config.AccessToken{
				{Name: "client", Value: downstreamSecret},
			},
		},
	}
}

func echoPathHandler(name string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, name+":"+request.URL.Path)
	})
}

func proxyOnce(t *testing.T, runtime *Runtime) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://proxy.test/api", nil)
	request.Header.Set("Authorization", "Bearer "+downstreamSecret)
	response := httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func assertHealth(t *testing.T, runtime *Runtime, method string, status int, bodyFragment string) {
	t.Helper()
	request := httptest.NewRequest(method, "http://proxy.test/healthz", nil)
	request.Header.Set("Authorization", "Bearer invalid-and-ignored")
	response := httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("health status = %d, want %d", response.Code, status)
	}
	if bodyFragment == "" && response.Body.Len() != 0 {
		t.Fatalf("health body = %q, want empty", response.Body.String())
	}
	if bodyFragment != "" && !strings.Contains(response.Body.String(), bodyFragment) {
		t.Fatalf("health body = %q, want fragment %q", response.Body.String(), bodyFragment)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
