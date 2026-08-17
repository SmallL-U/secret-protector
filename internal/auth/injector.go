package auth

import (
	"errors"
	"net/http"

	"secret-protector/internal/config"
)

type Injector interface {
	Inject(*http.Request, Credential)
}

func NewInjector(authConfig config.UpstreamAuth) (Injector, error) {
	switch authConfig.Mode {
	case "bearer":
		return bearerInjector{token: authConfig.Token}, nil
	case "query":
		return queryInjector{token: authConfig.Token, param: authConfig.QueryParam}, nil
	case "basic":
		return basicInjector{username: authConfig.Username, password: authConfig.Password}, nil
	case "auto":
		return autoInjector{
			token:      authConfig.Token,
			username:   authConfig.Username,
			password:   authConfig.Password,
			queryParam: authConfig.QueryParam,
		}, nil
	default:
		return nil, errors.New("unsupported upstream authentication mode")
	}
}

type bearerInjector struct {
	token string
}

func (injector bearerInjector) Inject(request *http.Request, _ Credential) {
	request.Header.Set("Authorization", "Bearer "+injector.token)
}

type queryInjector struct {
	token string
	param string
}

func (injector queryInjector) Inject(request *http.Request, _ Credential) {
	query := request.URL.Query()
	query.Set(injector.param, injector.token)
	request.URL.RawQuery = query.Encode()
}

type basicInjector struct {
	username string
	password string
}

func (injector basicInjector) Inject(request *http.Request, _ Credential) {
	request.SetBasicAuth(injector.username, injector.password)
}

type autoInjector struct {
	token      string
	username   string
	password   string
	queryParam string
}

func (injector autoInjector) Inject(request *http.Request, downstream Credential) {
	switch downstream.Scheme {
	case SchemeBearer:
		bearerInjector{token: injector.token}.Inject(request, downstream)
	case SchemeQuery:
		param := firstNonEmpty(injector.queryParam, downstream.QueryParam)
		queryInjector{token: injector.token, param: param}.Inject(request, downstream)
	case SchemeBasic:
		username := firstNonEmpty(injector.username, downstream.Username)
		password := firstNonEmpty(injector.password, injector.token)
		basicInjector{username: username, password: password}.Inject(request, downstream)
	default:
		return
	}
}

func firstNonEmpty(preferred string, fallback string) string {
	if preferred != "" {
		return preferred
	}

	return fallback
}
