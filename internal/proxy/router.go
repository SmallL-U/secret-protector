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
	"sort"
	"strings"

	"secret-protector/internal/auth"
	"secret-protector/internal/config"
)

type credentialContextKey struct{}

type Router struct {
	routes []*route
}

type route struct {
	name        string
	pathPrefix  string
	stripPrefix bool
	queryParams []string
	tokenHashes [][sha256.Size]byte
	proxy       *httputil.ReverseProxy
}

func NewRouter(cfg *config.Config, logger *slog.Logger) (*Router, error) {
	if logger == nil {
		logger = slog.Default()
	}

	routes := make([]*route, 0, len(cfg.Routes))
	for i := range cfg.Routes {
		compiled, err := newRoute(cfg.Routes[i], logger)
		if err != nil {
			return nil, err
		}
		routes = append(routes, compiled)
	}
	sort.SliceStable(routes, func(i, j int) bool {
		return len(routes[i].pathPrefix) > len(routes[j].pathPrefix)
	})

	return &Router{routes: routes}, nil
}

func (router *Router) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	for _, candidate := range router.routes {
		if !matchesPrefix(request.URL.Path, candidate.pathPrefix) {
			continue
		}
		candidate.serveHTTP(writer, request)
		return
	}

	writeError(writer, http.StatusNotFound, "route_not_found", "no route matches this request")
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
		pathPrefix:  routeConfig.PathPrefix,
		stripPrefix: routeConfig.Upstream.StripPrefix,
		queryParams: append([]string(nil), routeConfig.Downstream.QueryParams...),
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
		if compiled.stripPrefix {
			stripURLPathPrefix(request.URL, compiled.pathPrefix)
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

func (route *route) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	credential, err := auth.Detect(request, route.queryParams)
	if errors.Is(err, auth.ErrMissing) {
		writeUnauthorized(writer)
		return
	}
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_authentication", "authentication credentials are invalid")
		return
	}
	if !route.accepts(credential.Token) {
		writeUnauthorized(writer)
		return
	}

	ctx := context.WithValue(request.Context(), credentialContextKey{}, credential)
	route.proxy.ServeHTTP(writer, request.WithContext(ctx))
}

func (route *route) accepts(candidate string) bool {
	candidateHash := sha256.Sum256([]byte(candidate))
	matched := 0
	for _, expectedHash := range route.tokenHashes {
		matched |= subtle.ConstantTimeCompare(candidateHash[:], expectedHash[:])
	}

	return matched == 1
}

func matchesPrefix(requestPath string, prefix string) bool {
	if prefix == "/" {
		return true
	}
	if requestPath == prefix {
		return true
	}

	return strings.HasPrefix(requestPath, prefix+"/")
}

func stripPathPrefix(requestPath string, prefix string) string {
	if prefix == "/" {
		return requestPath
	}

	stripped := strings.TrimPrefix(requestPath, prefix)
	if stripped == "" {
		return "/"
	}

	return stripped
}

func stripURLPathPrefix(requestURL *url.URL, prefix string) {
	if prefix == "/" {
		return
	}

	originalEscapedPath := requestURL.EscapedPath()
	requestURL.Path = stripPathPrefix(requestURL.Path, prefix)
	if requestURL.RawPath == "" {
		return
	}

	prefixURL := &url.URL{Path: prefix}
	escapedPrefix := prefixURL.EscapedPath()
	if !strings.HasPrefix(originalEscapedPath, escapedPrefix) {
		requestURL.RawPath = ""
		return
	}

	rawPath := strings.TrimPrefix(originalEscapedPath, escapedPrefix)
	if rawPath == "" {
		rawPath = "/"
	}
	candidate := &url.URL{Path: requestURL.Path, RawPath: rawPath}
	if candidate.EscapedPath() != rawPath {
		requestURL.RawPath = ""
		return
	}
	requestURL.RawPath = rawPath
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
