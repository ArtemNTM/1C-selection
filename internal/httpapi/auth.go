package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/bochkarevart/1c-selection/internal/model"
)

type clientContextKey struct{}

func (a *api) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 512 {
			writeError(w, model.NewError("unauthorized", "Bearer session token is required; complete /hello first"))
			return
		}
		client, err := a.service.AuthenticateSession(r.Context(), parts[1])
		if err != nil {
			writeError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientContextKey{}, client)))
	})
}

func requireEditor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentClient(r.Context()).Role != "editor" {
			writeError(w, model.NewError("forbidden", "editor role is required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func currentClient(ctx context.Context) model.Client {
	client, _ := ctx.Value(clientContextKey{}).(model.Client)
	return client
}
