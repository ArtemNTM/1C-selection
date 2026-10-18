package httpapi

import (
	"context"
	"net/http"

	"github.com/bochkarevart/1c-selection/internal/model"
)

func (a *api) healthEndpoint() http.HandlerFunc {
	type Params struct{}
	return Wrap(func(_ context.Context, _ *Params) (any, error) {
		return map[string]string{"status": "ok"}, nil
	}, EndpointConfig{})
}

func (a *api) helloEndpoint() http.HandlerFunc {
	return Wrap(func(ctx context.Context, params *model.HelloRequest) (any, error) {
		if params.ClientID == "" || params.Token == "" || len(params.Token) > 512 {
			return nil, model.NewError("invalid", "client_id and token are required")
		}
		return a.service.CreateSession(ctx, params.ClientID, params.Token)
	}, EndpointConfig{Body: true})
}

func (a *api) noteEndpoint() http.HandlerFunc {
	return Wrap(func(ctx context.Context, params *model.NoteRequest) (model.Receipt, error) {
		return a.service.SubmitNote(ctx, currentClient(ctx).ID, *params)
	}, EndpointConfig{Body: true})
}

func (a *api) notificationsEndpoint() http.HandlerFunc {
	type Params struct {
		Limit int `query:"limit" default:"100" min:"1" max:"1000" json:"-"`
	}
	return Wrap(func(ctx context.Context, params *Params) (any, error) {
		values, err := a.service.Notifications(ctx, currentClient(ctx).ID, params.Limit)
		if values == nil {
			values = []model.Notification{}
		}
		return values, err
	}, EndpointConfig{})
}

func (a *api) ackEndpoint() http.HandlerFunc {
	type Params struct {
		ID int64 `path:"id" json:"-" min:"1"`
	}
	return Wrap(func(ctx context.Context, params *Params) (any, error) {
		return nil, a.service.AckNotification(ctx, currentClient(ctx).ID, params.ID)
	}, EndpointConfig{Status: http.StatusNoContent})
}

func (a *api) myStatsEndpoint() http.HandlerFunc {
	type Params struct{}
	return Wrap(func(ctx context.Context, _ *Params) (any, error) {
		return a.service.Stats(ctx, currentClient(ctx).ID)
	}, EndpointConfig{})
}

func (a *api) allStatsEndpoint() http.HandlerFunc {
	type Params struct{}
	return Wrap(func(ctx context.Context, _ *Params) (any, error) {
		values, err := a.service.AllStats(ctx)
		if values == nil {
			values = []model.Stats{}
		}
		return values, err
	}, EndpointConfig{})
}

func (a *api) clientsEndpoint() http.HandlerFunc {
	type Params struct{}
	return Wrap(func(ctx context.Context, _ *Params) (any, error) {
		values, err := a.service.ListClients(ctx)
		if values == nil {
			values = []model.Client{}
		}
		return values, err
	}, EndpointConfig{})
}

func (a *api) createClientEndpoint() http.HandlerFunc {
	return Wrap(func(ctx context.Context, params *model.CreateClientRequest) (model.ClientCredentials, error) {
		return a.service.CreateClient(ctx, *params)
	}, EndpointConfig{
		Body:   true,
		Status: http.StatusCreated,
	})
}

func (a *api) updateClientEndpoint() http.HandlerFunc {
	type Params struct {
		ID int64 `path:"id" json:"-" min:"1"`
		model.UpdateClientRequest
	}
	return Wrap(func(ctx context.Context, params *Params) (model.Client, error) {
		return a.service.UpdateClient(ctx, params.ID, params.UpdateClientRequest)
	}, EndpointConfig{Body: true})
}

func (a *api) storiesEndpoint() http.HandlerFunc {
	type Params struct {
		Limit int `query:"limit" default:"100" min:"1" max:"1000" json:"-"`
	}
	return Wrap(func(ctx context.Context, params *Params) (any, error) {
		values, err := a.service.Stories(ctx, params.Limit)
		if values == nil {
			values = []model.Story{}
		}
		return values, err
	}, EndpointConfig{})
}

func (a *api) storyEndpoint() http.HandlerFunc {
	type Params struct {
		ID int64 `path:"id" json:"-" min:"1"`
	}
	return Wrap(func(ctx context.Context, params *Params) (any, error) {
		return a.service.Story(ctx, params.ID)
	}, EndpointConfig{})
}
