package service

import (
	"context"
	"math"
	"testing"

	"github.com/bochkarevart/1c-selection/internal/model"
)

func TestCreateClientRejectsInvalidRequestBeforeDatabaseAccess(t *testing.T) {
	valid := model.CreateClientRequest{
		Name:  "Reporter",
		Kind:  "staff",
		Role:  "reporter",
		Trust: 0.5,
	}

	tests := []struct {
		name string
		edit func(*model.CreateClientRequest)
	}{
		{
			name: "NUL in name",
			edit: func(req *model.CreateClientRequest) {
				req.Name = "Report\x00er"
			},
		},
		{
			name: "unknown kind",
			edit: func(req *model.CreateClientRequest) {
				req.Kind = "contractor"
			},
		},
		{
			name: "unknown role",
			edit: func(req *model.CreateClientRequest) {
				req.Role = "publisher"
			},
		},
		{
			name: "NaN trust",
			edit: func(req *model.CreateClientRequest) {
				req.Trust = math.NaN()
			},
		},
		{
			name: "positive infinite trust",
			edit: func(req *model.CreateClientRequest) {
				req.Trust = math.Inf(1)
			},
		},
		{
			name: "negative infinite trust",
			edit: func(req *model.CreateClientRequest) {
				req.Trust = math.Inf(-1)
			},
		},
	}

	svc := New(nil, nil, DefaultConfig())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			tt.edit(&req)

			got, err := svc.CreateClient(context.Background(), req)
			if err == nil {
				t.Fatal("CreateClient() error = nil, want invalid error")
			}
			apiErr, ok := err.(*model.Error)
			if !ok || apiErr.Code != "invalid" {
				t.Fatalf("CreateClient() error = %T %v, want model invalid error", err, err)
			}
			if got != (model.ClientCredentials{}) {
				t.Fatalf("CreateClient() result = %#v, want empty credentials", got)
			}
		})
	}
}
