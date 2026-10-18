package httpapi

import (
	"context"
	"errors"
	"net/http"

	apidocs "github.com/bochkarevart/1c-selection/api"
	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/go-chi/chi/v5"
)

type Service interface {
	CreateClient(context.Context, model.CreateClientRequest) (model.ClientCredentials, error)
	ClientByID(context.Context, int64) (model.Client, error)
	CreateSession(context.Context, string, string) (model.HelloResponse, error)
	AuthenticateSession(context.Context, string) (model.Client, error)
	UpdateClient(context.Context, int64, model.UpdateClientRequest) (model.Client, error)
	ListClients(context.Context) ([]model.Client, error)
	SubmitNote(context.Context, int64, model.NoteRequest) (model.Receipt, error)
	Notifications(context.Context, int64, int) ([]model.Notification, error)
	AckNotification(context.Context, int64, int64) error
	Stats(context.Context, int64) (model.Stats, error)
	AllStats(context.Context) ([]model.Stats, error)
	Stories(context.Context, int) ([]model.Story, error)
	Story(context.Context, int64) (model.Story, error)
}

type api struct {
	service Service
}

func New(service Service) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("service is required")
	}
	return newHandler(service)
}

func newHandler(service Service) (http.Handler, error) {
	a := &api{service: service}
	r := chi.NewRouter()
	r.Use(securityHeaders)
	if err := apidocs.Register(r); err != nil {
		return nil, err
	}
	a.routes(r)
	return r, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Config", "nosniff")
		next.ServeHTTP(w, r)
	})
}
