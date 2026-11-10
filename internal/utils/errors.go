package utils

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// LoggerContextKey is where middleware.RequestID keeps the request's logger.
const LoggerContextKey = "logger"

// ErrorBody is the "error" part of the response envelope.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// AppError is a typed application error that carries an HTTP status and a
// machine-readable code, so handlers can return domain errors from services
// without services knowing about Gin.
type AppError struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e *AppError) Error() string { return e.Message }

func NewAppError(status int, code, message string) *AppError {
	return &AppError{Status: status, Code: code, Message: message}
}

func NewValidationError(fields map[string]string) *AppError {
	return &AppError{
		Status:  http.StatusUnprocessableEntity,
		Code:    "VALIDATION_ERROR",
		Message: "One or more fields are invalid.",
		Fields:  fields,
	}
}

// Common, reused domain errors.
var (
	ErrUnauthorized     = NewAppError(http.StatusUnauthorized, "UNAUTHORIZED", "Invalid credentials or session expired.")
	ErrForbidden        = NewAppError(http.StatusForbidden, "FORBIDDEN", "You do not have permission to perform this action.")
	ErrNotFound         = NewAppError(http.StatusNotFound, "NOT_FOUND", "The requested resource was not found.")
	ErrAccountLocked    = NewAppError(http.StatusTooManyRequests, "ACCOUNT_LOCKED", "Too many failed attempts. Try again later.")
	ErrAccountDisabled  = NewAppError(http.StatusForbidden, "ACCOUNT_DISABLED", "This account has been disabled.")
	ErrInsufficientCash = NewAppError(http.StatusUnprocessableEntity, "INSUFFICIENT_CASH", "Amount received is less than the amount due.")
	ErrInternal         = NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Something went wrong. Please try again.")
	ErrRateLimited      = NewAppError(http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests. Please wait a moment and try again.")
	ErrPayloadTooLarge  = NewAppError(http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "That request is too large.")
)

// RespondError writes an AppError using the standard envelope. Any other error
// is unexpected: it's logged in full, and the client gets a generic message so
// SQL, file paths and the like never leave the server.
func RespondError(c *gin.Context, err error) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		requestLogger(c).Error().Err(err).Msg("request failed")
		appErr = ErrInternal
	}
	c.JSON(appErr.Status, Envelope{
		Success: false,
		Data:    nil,
		Error: &ErrorBody{
			Code:    appErr.Code,
			Message: appErr.Message,
			Fields:  appErr.Fields,
		},
	})
}

func requestLogger(c *gin.Context) *zerolog.Logger {
	if v, ok := c.Get(LoggerContextKey); ok {
		if l, ok := v.(zerolog.Logger); ok {
			return &l
		}
	}
	return &log.Logger
}
