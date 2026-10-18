package httpapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bochkarevart/1c-selection/internal/model"
)

type wrapperTestField struct {
	value string
}

func (f *wrapperTestField) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	f.value = "parsed:" + value
	return nil
}

type wrapperTestParams struct{}

func TestWrapWritesConfiguredSuccessAndModelError(t *testing.T) {
	t.Run("configured JSON status", func(t *testing.T) {
		handler := Wrap(func(context.Context, *wrapperTestParams) (any, error) {
			return struct {
				Name string `json:"name"`
			}{Name: "created"}, nil
		}, EndpointConfig{Status: http.StatusCreated})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
		if response.Code != http.StatusCreated || response.Body.String() != "{\"name\":\"created\"}\n" {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Fatalf("Content-Type = %q", got)
		}
	})

	t.Run("no content", func(t *testing.T) {
		handler := Wrap(func(context.Context, *wrapperTestParams) (any, error) {
			return map[string]string{"ignored": "value"}, nil
		}, EndpointConfig{Status: http.StatusNoContent})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/", nil))
		if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
			t.Fatalf("response = %d %q, want 204 with empty body", response.Code, response.Body.String())
		}
	})

	t.Run("model invalid error", func(t *testing.T) {
		handler := Wrap(func(context.Context, *wrapperTestParams) (any, error) {
			return nil, model.NewError("invalid", "bad input")
		}, EndpointConfig{})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid"`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
	})
}

func TestWrapAcceptsConcreteResponse(t *testing.T) {
	handler := Wrap(func(context.Context, *wrapperTestParams) (model.Receipt, error) {
		return model.Receipt{
			Status: "accepted",
			NoteID: 7,
		}, nil
	}, EndpointConfig{})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", response.Code, response.Body.String())
	}
	var receipt model.Receipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "accepted" || receipt.NoteID != 7 {
		t.Fatalf("receipt = %+v, want accepted note 7", receipt)
	}
}

func TestWrapMarshalFailureIsSanitizedBeforeHeaders(t *testing.T) {
	handler := Wrap(func(context.Context, *wrapperTestParams) (any, error) {
		return struct {
			N float64 `json:"n"`
		}{N: math.NaN()}, nil
	}, EndpointConfig{Status: http.StatusCreated})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body %q; marshal failure must not retain success status", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "NaN") || strings.Contains(response.Body.String(), "created") || !strings.Contains(response.Body.String(), `"code":"internal"`) {
		t.Fatalf("unsanitized marshal failure body %q", response.Body.String())
	}
}

func TestWrapBodyUsesJSONUnmarshalerOnParams(t *testing.T) {
	type params struct {
		Field wrapperTestField `json:"field"`
	}
	called := false
	handler := Wrap(func(_ context.Context, request *params) (any, error) {
		called = true
		if request.Field.value != "parsed:source" {
			return nil, model.NewError("invalid", "custom JSON unmarshaler was not called")
		}
		return map[string]bool{"ok": true}, nil
	}, EndpointConfig{Body: true})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"field":"source"}`)))
	if !called || response.Code != http.StatusOK {
		t.Fatalf("called = %v, response = %d %q", called, response.Code, response.Body.String())
	}
}
