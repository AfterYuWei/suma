package outbound

import (
	"context"
	"errors"
	"net/http"
)

type plainHTTPKey struct{}
type guardedTransport struct {
	base            *http.Transport
	allowHTTP       bool
	privateHTTPOnly bool
}

func (t guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := Validate(req.URL.String(), t.allowHTTP); err != nil {
		return nil, err
	}
	if req.URL.Scheme == "http" {
		if !t.allowHTTP {
			return nil, errors.New("HTTP requires explicit permission")
		}
		if t.privateHTTPOnly {
			req = req.Clone(context.WithValue(req.Context(), plainHTTPKey{}, true))
		}
	}
	return t.base.RoundTrip(req)
}
