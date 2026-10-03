package outbound

import (
	"context"
	"errors"
	"net/http"
)

type plainHTTPKey struct{}
type guardedTransport struct {
	base         *http.Transport
	allowPrivate bool
}

func (t guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := Validate(req.URL.String(), t.allowPrivate); err != nil {
		return nil, err
	}
	if req.URL.Scheme == "http" {
		if !t.allowPrivate {
			return nil, errors.New("HTTP requires an explicit private endpoint")
		}
		req = req.Clone(context.WithValue(req.Context(), plainHTTPKey{}, true))
	}
	return t.base.RoundTrip(req)
}
