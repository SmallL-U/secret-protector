package auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

type Scheme string

const (
	SchemeQuery  Scheme = "query"
	SchemeBearer Scheme = "bearer"
	SchemeBasic  Scheme = "basic"
	SchemeHeader Scheme = "header"
)

var (
	ErrMissing     = errors.New("authentication credentials are missing")
	ErrMalformed   = errors.New("authentication credentials are malformed")
	ErrAmbiguous   = errors.New("multiple authentication credentials were provided")
	ErrUnsupported = errors.New("authorization scheme is not supported")
)

type Credential struct {
	Scheme     Scheme
	Token      string
	Username   string
	QueryParam string
	HeaderName string
}

func Detect(request *http.Request, queryParams []string, headerNames []string) (Credential, error) {
	credentials := make([]Credential, 0, 2)
	headerCredential, hasHeader, err := detectAuthorizationHeader(request.Header.Values("Authorization"))
	if err != nil {
		return Credential{}, err
	}
	if hasHeader {
		credentials = append(credentials, headerCredential)
	}

	for _, headerName := range headerNames {
		values := request.Header.Values(headerName)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 || values[0] == "" {
			return Credential{}, ErrMalformed
		}
		credentials = append(credentials, Credential{
			Scheme:     SchemeHeader,
			Token:      values[0],
			HeaderName: headerName,
		})
	}

	query := request.URL.Query()
	for _, param := range queryParams {
		values, exists := query[param]
		if !exists {
			continue
		}
		if len(values) != 1 || values[0] == "" {
			return Credential{}, ErrMalformed
		}
		credentials = append(credentials, Credential{
			Scheme:     SchemeQuery,
			Token:      values[0],
			QueryParam: param,
		})
	}

	if len(credentials) == 0 {
		return Credential{}, ErrMissing
	}
	if len(credentials) > 1 {
		return Credential{}, ErrAmbiguous
	}

	return credentials[0], nil
}

func detectAuthorizationHeader(values []string) (Credential, bool, error) {
	if len(values) == 0 {
		return Credential{}, false, nil
	}
	if len(values) > 1 {
		return Credential{}, false, ErrAmbiguous
	}

	credential, err := detectHeader(values[0])
	if err != nil {
		return Credential{}, false, err
	}

	return credential, true, nil
}

func detectHeader(value string) (Credential, error) {
	scheme, encoded, found := strings.Cut(value, " ")
	if !found {
		return Credential{}, ErrUnsupported
	}
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return Credential{}, ErrMalformed
	}

	switch {
	case strings.EqualFold(scheme, "Bearer"):
		if strings.ContainsAny(encoded, " \t\r\n") {
			return Credential{}, ErrMalformed
		}
		return Credential{Scheme: SchemeBearer, Token: encoded}, nil
	case strings.EqualFold(scheme, "Basic"):
		return detectBasic(encoded)
	default:
		return Credential{}, ErrUnsupported
	}
}

func detectBasic(encoded string) (Credential, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Credential{}, ErrMalformed
	}

	username, password, found := strings.Cut(string(decoded), ":")
	if !found || password == "" {
		return Credential{}, ErrMalformed
	}

	return Credential{
		Scheme:   SchemeBasic,
		Token:    password,
		Username: username,
	}, nil
}
