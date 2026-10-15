package apierror

import (
	"encoding/json"
	"net/http"
)

type codedError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type body struct {
	Error codedError `json:"error"`
}

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(code, message string) *Error {
	return New(http.StatusBadRequest, code, message)
}

func Unauthorized(code, message string) *Error {
	return New(http.StatusUnauthorized, code, message)
}

func Forbidden(code, message string) *Error {
	return New(http.StatusForbidden, code, message)
}

func NotFound(code, message string) *Error {
	return New(http.StatusNotFound, code, message)
}

func Conflict(code, message string) *Error {
	return New(http.StatusConflict, code, message)
}

func TooLarge(code, message string) *Error {
	return New(http.StatusRequestEntityTooLarge, code, message)
}

func TooManyRequests() *Error {
	return New(http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
}

func Internal() *Error {
	return New(http.StatusInternalServerError, "internal", "something went wrong")
}

func Unavailable() *Error {
	return New(http.StatusServiceUnavailable, "unavailable", "service temporarily unavailable")
}

func Write(w http.ResponseWriter, r *http.Request, err error) {
	apiErr, ok := err.(*Error)
	if !ok {
		apiErr = Internal()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apiErr.Status)
	_ = json.NewEncoder(w).Encode(body{Error: codedError{Code: apiErr.Code, Message: apiErr.Message}})
}
