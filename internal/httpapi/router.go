package httpapi

import "github.com/go-chi/chi/v5"

func (a *api) routes(r chi.Router) {
	r.Get("/healthz", a.healthEndpoint())
	r.Post("/hello", a.helloEndpoint())

	r.Group(func(r chi.Router) {
		r.Use(a.auth)
		r.Post("/notes", a.noteEndpoint())
		r.Get("/notifications", a.notificationsEndpoint())
		r.Post("/notifications/{id}/ack", a.ackEndpoint())
		r.Get("/stats/me", a.myStatsEndpoint())
		r.Get("/stories", a.storiesEndpoint())
		r.Get("/stories/{id}", a.storyEndpoint())

		r.Group(func(r chi.Router) {
			r.Use(requireEditor)
			r.Get("/stats", a.allStatsEndpoint())
			r.Get("/clients", a.clientsEndpoint())
			r.Post("/clients", a.createClientEndpoint())
			r.Patch("/clients/{id}", a.updateClientEndpoint())
		})
	})
}
