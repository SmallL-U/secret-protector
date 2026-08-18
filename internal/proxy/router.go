package proxy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"secret-protector/internal/auth"
	"secret-protector/internal/config"
)

type credentialContextKey struct{}

type Router struct {
	routes      []*route
	queryParams []string
	headerNames []string
}

type route struct {
	name        string
	queryParams []string
	headerNames []string
	tokenHashes [][sha256.Size]byte
	proxy       *httputil.ReverseProxy
}

func NewRouter(cfg *config.Config, logger *slog.Logger) (*Router, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	routes := make([]*route, 0, len(cfg.Routes))
	queryParams := make([]string, 0)
	headerNames := make([]string, 0)
	knownQueryParams := make(map[string]struct{})
	knownHeaderNames := make(map[string]struct{})
	for i := range cfg.Routes {
		compiled, err := newRoute(cfg.Routes[i], logger)
		if err != nil {
			return nil, err
		}
		routes = append(routes, compiled)
		for _, param := range compiled.queryParams {
			if _, exists := knownQueryParams[param]; exists {
				continue
			}
			knownQueryParams[param] = struct{}{}
			queryParams = append(queryParams, param)
		}
		for _, name := range compiled.headerNames {
			canonical := http.CanonicalHeaderKey(name)
			if _, exists := knownHeaderNames[canonical]; exists {
				continue
			}
			knownHeaderNames[canonical] = struct{}{}
			headerNames = append(headerNames, canonical)
		}
	}

	return &Router{routes: routes, queryParams: queryParams, headerNames: headerNames}, nil
}

func (router *Router) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	credential, err := auth.Detect(request, router.queryParams, router.headerNames)
	if errors.Is(err, auth.ErrMissing) {
		writeUnauthorized(writer)
		return
	}
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_authentication", "authentication credentials are invalid")
		return
	}

	matched := router.matchCredential(credential)
	if matched == nil {
		writeUnauthorized(writer)
		return
	}

	ctx := context.WithValue(request.Context(), credentialContextKey{}, credential)
	matched.proxy.ServeHTTP(writer, request.WithContext(ctx))
}

func newRoute(routeConfig config.Route, logger *slog.Logger) (*route, error) {
	target, err := url.Parse(routeConfig.Upstream.URL)
	if err != nil {
		return nil, errors.New("build route: invalid upstream URL")
	}
	injector, err := auth.NewInjector(routeConfig.Upstream.Auth)
	if err != nil {
		return nil, err
	}

	compiled := &route{
		name:        routeConfig.Name,
		queryParams: append([]string(nil), routeConfig.Downstream.QueryParams...),
		headerNames: append([]string(nil), routeConfig.Downstream.Headers...),
		tokenHashes: make([][sha256.Size]byte, len(routeConfig.Downstream.Tokens)),
	}
	for i, accessToken := range routeConfig.Downstream.Tokens {
		compiled.tokenHashes[i] = sha256.Sum256([]byte(accessToken.Value))
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := reverseProxy.Director
	reverseProxy.Director = func(request *http.Request) {
		downstream, _ := request.Context().Value(credentialContextKey{}).(auth.Credential)
		request.Header.Del("Authorization")
		if downstream.Scheme == auth.SchemeQuery {
			query := request.URL.Query()
			query.Del(downstream.QueryParam)
			request.URL.RawQuery = query.Encode()
		}
		if downstream.Scheme == auth.SchemeHeader {
			request.Header.Del(downstream.HeaderName)
		}

		baseDirector(request)
		request.Host = target.Host
		injector.Inject(request, downstream)
	}
	reverseProxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, proxyError error) {
		logger.Warn("upstream request failed", "route", compiled.name, "path", request.URL.Path, "error", proxyError)
		writeError(writer, http.StatusBadGateway, "upstream_unavailable", "the upstream request failed")
	}
	compiled.proxy = reverseProxy

	return compiled, nil
}

func (router *Router) matchCredential(credential auth.Credential) *route {
	var matched *route
	matchCount := 0
	for _, candidate := range router.routes {
		if !candidate.accepts(credential.Token) {
			continue
		}
		matched = candidate
		matchCount++
	}
	if matchCount != 1 {
		return nil
	}
	if credential.Scheme == auth.SchemeQuery && !matched.acceptsQueryParam(credential.QueryParam) {
		return nil
	}
	if credential.Scheme == auth.SchemeHeader && !matched.acceptsHeaderName(credential.HeaderName) {
		return nil
	}

	return matched
}

func (route *route) accepts(candidate string) bool {
	candidateHash := sha256.Sum256([]byte(candidate))
	matched := 0
	for _, expectedHash := range route.tokenHashes {
		matched |= subtle.ConstantTimeCompare(candidateHash[:], expectedHash[:])
	}

	return matched == 1
}

func (route *route) acceptsQueryParam(candidate string) bool {
	for _, expected := range route.queryParams {
		if candidate == expected {
			return true
		}
	}

	return false
}

func (route *route) acceptsHeaderName(candidate string) bool {
	canonical := http.CanonicalHeaderKey(candidate)
	for _, expected := range route.headerNames {
		if canonical == http.CanonicalHeaderKey(expected) {
			return true
		}
	}

	return false
}

func writeUnauthorized(writer http.ResponseWriter) {
	writer.Header().Set("WWW-Authenticate", `Bearer realm="secret-protector"`)
	writeError(writer, http.StatusUnauthorized, "authentication_failed", "authentication failed")
}

func writeError(writer http.ResponseWriter, status int, code string, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{
		"error":   code,
		"message": message,
	})
}
