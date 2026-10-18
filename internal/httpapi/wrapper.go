package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/go-chi/chi/v5"
)

const maxRequestBytes = 64 << 10

type EndpointConfig struct {
	Body   bool
	Status int
}

func Wrap[Params, Response any](callback func(context.Context, *Params) (Response, error), cfg EndpointConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := new(Params)
		if cfg.Body && !decodeJSON(w, r, params) {
			return
		}
		if err := bindFields(params, r); err != nil {
			writeError(w, err)
			return
		}
		result, err := callback(r.Context(), params)
		if err != nil {
			writeError(w, err)
			return
		}
		status := cfg.Status
		if status == 0 {
			status = http.StatusOK
		}
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, status, result)
	}
}

func bindFields(params any, r *http.Request) error {
	value := reflect.ValueOf(params).Elem()
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := field.Tag.Get("path")
		raw := ""
		if name != "" {
			raw = chi.URLParam(r, name)
		} else if name = field.Tag.Get("query"); name != "" {
			raw = r.URL.Query().Get(name)
			if raw == "" {
				raw = field.Tag.Get("default")
			}
		} else {
			continue
		}
		if raw == "" {
			continue
		}
		number, err := strconv.ParseInt(raw, 10, field.Type.Bits())
		if err != nil {
			return model.NewError("invalid", name+" must be a number")
		}
		if rawMin := field.Tag.Get("min"); rawMin != "" {
			minimum, _ := strconv.ParseInt(rawMin, 10, 64)
			if number < minimum {
				return model.NewError("invalid", fmt.Sprintf("%s must be at least %d", name, minimum))
			}
		}
		if rawMax := field.Tag.Get("max"); rawMax != "" {
			maximum, _ := strconv.ParseInt(rawMax, 10, 64)
			if number > maximum {
				return model.NewError("invalid", fmt.Sprintf("%s must be at most %d", name, maximum))
			}
		}
		value.Field(i).SetInt(number)
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if contentType := r.Header.Get("Content-Type"); contentType != "" && !isJSONContentType(contentType) {
		writeError(w, model.NewError("invalid", "Content-Type must be application/json"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSONDecodeError(w, err)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			writeError(w, model.NewError("invalid", "request must contain exactly one JSON value"))
		} else {
			writeJSONDecodeError(w, err)
		}
		return false
	}
	return true
}

func isJSONContentType(value string) bool {
	return strings.EqualFold(strings.TrimSpace(strings.Split(value, ";")[0]), "application/json")
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		writeErrorStatus(w, http.StatusRequestEntityTooLarge, &model.Error{
			Code:    "invalid",
			Message: "request body is too large",
		})
		return
	}
	writeError(w, model.NewError("invalid", "invalid JSON: "+err.Error()))
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	payload := &model.Error{
		Code:    "internal",
		Message: "internal server error",
	}
	var modelErr *model.Error
	if errors.As(err, &modelErr) {
		switch modelErr.Code {
		case "invalid":
			status = http.StatusBadRequest
		case "unauthorized":
			status = http.StatusUnauthorized
		case "forbidden":
			status = http.StatusForbidden
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		}
		if status != http.StatusInternalServerError {
			payload = modelErr
		}
	}
	if status == http.StatusInternalServerError {
		log.Printf("httpapi: internal error: %v", err)
	}
	writeErrorStatus(w, status, payload)
}

func writeErrorStatus(w http.ResponseWriter, status int, payload *model.Error) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	writeJSON(w, status, map[string]any{"error": payload})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("httpapi: response marshal failed: %v", err)
		data, status = []byte(`{"error":{"code":"internal","message":"internal server error"}}`), http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
