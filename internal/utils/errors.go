package utils

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

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
)

// RespondError writes an AppError (or wraps a plain error as internal) using
// the standard envelope, and logs 5xx errors via the request-scoped logger.
func RespondError(c *gin.Context, err error) {
	appErr, ok := err.(*AppError)
	if !ok {
		appErr = &AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: err.Error()}
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
