/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package errors

import (
	"fmt"
	"net/http"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/fielderrors"
)

// HTTP Status codes not in the golang http package.
const (
	StatusUnprocessableEntity = 422
	StatusTooManyRequests     = 429
	// HTTP recommendations are for servers to define 5xx error codes
	// for scenarios not covered by behavior. In this case, ServerTimeout
	// is an indication that a transient server error has occured and the
	// client *should* retry, with an optional Retry-After header to specify
	// the back off window.
	StatusServerTimeout = 504
)

// StatusError is an error intended for consumption by a REST API server; it can also be
// reconstructed by clients from a REST response. Public to allow easy type switches.
type StatusError struct {
	ErrStatus api.Status
}

var _ error = &StatusError{}

// Error implements the Error interface.
func (e *StatusError) Error() string {
	return e.ErrStatus.Message
}

// Status allows access to e's status without having to know the detailed workings
// of StatusError. Used by pkg/apiserver.
func (e *StatusError) Status() api.Status {
	return e.ErrStatus
}

// UnexpectedObjectError can be returned by FromObject if it's passed a non-status object.
type UnexpectedObjectError struct {
	Object runtime.Object
}

// Error returns an error message describing 'u'.
func (u *UnexpectedObjectError) Error() string {
	return fmt.Sprintf("unexpected object: %v", u.Object)
}

// FromObject generates an StatusError from an api.Status, if that is the type of obj; otherwise,
// returns an UnexpecteObjectError.
func FromObject(obj runtime.Object) error {
	switch t := obj.(type) {
	case *api.Status:
		return &StatusError{*t}
	}
	return &UnexpectedObjectError{obj}
}

// NewNotFound returns a new error which indicates that the resource of the kind and the name was not found.
func NewNotFound(kind, name string) error {
	return &StatusError{api.Status{
		Status: api.StatusFailure,
		Code:   http.StatusNotFound,
		Reason: api.StatusReasonNotFound,
		Details: &api.StatusDetails{
			Kind: kind,
			ID:   name,
		},
		Message: fmt.Sprintf("%s %q not found", kind, name),
	}}
}

// NewAlreadyExists returns an error indicating the item requested exists by that identifier.
func NewAlreadyExists(kind, name string) error {
	return &StatusError{api.Status{
		Status: api.StatusFailure,
		Code:   http.StatusConflict,
		Reason: api.StatusReasonAlreadyExists,
		Details: &api.StatusDetails{
			Kind: kind,
			ID:   name,
		},
		Message: fmt.Sprintf("%s %q already exists", kind, name),
	}}
}

// NewUnauthorized returns an error indicating the client is not authorized to perform the requested
// action.
func NewUnauthorized(reason string) error {
	message := reason
	if len(message) == 0 {
		message = "not authorized"
	}
	return &StatusError{api.Status{
		Status:  api.StatusFailure,
		Code:    http.StatusUnauthorized,
		Reason:  api.StatusReasonUnauthorized,
		Message: message,
	}}
}

// NewForbidden returns an error indicating the requested action was forbidden
func NewForbidden(kind, name string, err error) error {
	return &StatusError{api.Status{
		Status: api.StatusFailure,
		Code:   http.StatusForbidden,
		Reason: api.StatusReasonForbidden,
		Details: &api.StatusDetails{
			Kind: kind,
			ID:   name,
		},
		Message: fmt.Sprintf("%s %q is forbidden: %v", kind, name, err),
	}}
}

// NewConflict returns an error indicating the item can't be updated as provided.
func NewConflict(kind, name string, err error) error {
	return &StatusError{api.Status{
		Status: api.StatusFailure,
		Code:   http.StatusConflict,
		Reason: api.StatusReasonConflict,
		Details: &api.StatusDetails{
			Kind: kind,
			ID:   name,
		},
		Message: fmt.Sprintf("%s %q cannot be updated: %v", kind, name, err),
	}}
}

// NewInvalid returns an error indicating the item is invalid and cannot be processed.
func NewInvalid(kind, name string, errs fielderrors.ValidationErrorList) error {
	causes := make([]api.StatusCause, 0, len(errs))
	for i := range errs {
		if err, ok := errs[i].(*fielderrors.ValidationError); ok {
			causes = append(causes, api.StatusCause{
				Type:    api.CauseType(err.Type),
				Message: err.Error(),
				Field:   err.Field,
			})
		}
	}
	return &StatusError{api.Status{
		Status: api.StatusFailure,
		Code:   StatusUnprocessableEntity, // RFC 4918: StatusUnprocessableEntity
		Reason: api.StatusReasonInvalid,
		Details: &api.StatusDetails{
			Kind:   kind,
			ID:     name,
			Causes: causes,
		},
		Message: fmt.Sprintf("%s %q is invalid: %v", kind, name, errors.NewAggregate(errs)),
	}}
}

// NewBadRequest creates an error that indicates that the request is invalid and can not be processed.
func NewBadRequest(reason string) error {
	return &StatusError{api.Status{
		Status:  api.StatusFailure,
		Code:    http.StatusBadRequest,
		Reason:  api.StatusReasonBadRequest,
		Message: reason,
	}}
}

// NewMethodNotSupported returns an error indicating the requested action is not supported on this kind.
func NewMethodNotSupported(kind, action string) error {
	return &StatusError{api.Status{
		Status: api.StatusFailure,
		Code:   http.StatusMethodNotAllowed,
		Reason: api.StatusReasonMethodNotAllowed,
		Details: &api.StatusDetails{
			Kind: kind,
		},
		Message: fmt.Sprintf("%s is not supported on resources of kind %q", action, kind),
	}}
}

// NewServerTimeout returns an error indicating the requested action could not be completed due to a
// transient error, and the client should try again.
func NewServerTimeout(kind, operation string, retryAfterSeconds int) error {
	return &StatusError{api.Status{
		Status: api.StatusFailure,
		Code:   http.StatusInternalServerError,
		Reason: api.StatusReasonServerTimeout,
		Details: &api.StatusDetails{
			Kind:              kind,
			ID:                operation,
			RetryAfterSeconds: retryAfterSeconds,
		},
		Message: fmt.Sprintf("The %s operation against %s could not be completed at this time, please try again.", operation, kind),
	}}
}

// NewInternalError returns an error indicating the item is invalid and cannot be processed.
func NewInternalError(err error) error {
	return &StatusError{api.Status{
		Status: api.StatusFailure,
		Code:   http.StatusInternalServerError,
		Reason: api.StatusReasonInternalError,
		Details: &api.StatusDetails{
			Causes: []api.StatusCause{{Message: err.Error()}},
		},
		Message: fmt.Sprintf("Internal error occurred: %v", err),
	}}
}

// NewTimeoutError returns an error indicating that a timeout occurred before the request
// could be completed.  Clients may retry, but the operation may still complete.
func NewTimeoutError(message string, retryAfterSeconds int) error {
	return &StatusError{api.Status{
		Status:  api.StatusFailure,
		Code:    StatusServerTimeout,
		Reason:  api.StatusReasonTimeout,
		Message: fmt.Sprintf("Timeout: %s", message),
		Details: &api.StatusDetails{
			RetryAfterSeconds: retryAfterSeconds,
		},
	}}
}

// IsNotFound returns true if the specified error was created by NewNotFoundErr.
func IsNotFound(err error) bool {
	return reasonForError(err) == api.StatusReasonNotFound
}

// IsAlreadyExists determines if the err is an error which indicates that a specified resource already exists.
func IsAlreadyExists(err error) bool {
	return reasonForError(err) == api.StatusReasonAlreadyExists
}

// IsConflict determines if the err is an error which indicates the provided update conflicts.
func IsConflict(err error) bool {
	return reasonForError(err) == api.StatusReasonConflict
}

// IsInvalid determines if the err is an error which indicates the provided resource is not valid.
func IsInvalid(err error) bool {
	return reasonForError(err) == api.StatusReasonInvalid
}

// IsMethodNotSupported determines if the err is an error which indicates the provided action could not
// be performed because it is not supported by the server.
func IsMethodNotSupported(err error) bool {
	return reasonForError(err) == api.StatusReasonMethodNotAllowed
}

// IsBadRequest determines if err is an error which indicates that the request is invalid.
func IsBadRequest(err error) bool {
	return reasonForError(err) == api.StatusReasonBadRequest
}

// IsUnauthorized determines if err is an error which indicates that the request is unauthorized and
// requires authentication by the user.
func IsUnauthorized(err error) bool {
	return reasonForError(err) == api.StatusReasonUnauthorized
}

// IsForbidden determines if err is an error which indicates that the request is forbidden and cannot
// be completed as requested.
func IsForbidden(err error) bool {
	return reasonForError(err) == api.StatusReasonForbidden
}

// IsServerTimeout determines if err is an error which indicates that the request needs to be retried
// by the client.
func IsServerTimeout(err error) bool {
	return reasonForError(err) == api.StatusReasonServerTimeout
}

// IsStatusError determines if err is an API Status error received from the master.
func IsStatusError(err error) bool {
	_, ok := err.(*StatusError)
	return ok
}

// IsUnexpectedObjectError determines if err is due to an unexpected object from the master.
func IsUnexpectedObjectError(err error) bool {
	_, ok := err.(*UnexpectedObjectError)
	return ok
}

// SuggestsClientDelay returns true if this error suggests a client delay as well as the
// suggested seconds to wait, or false if the error does not imply a wait.
func SuggestsClientDelay(err error) (int, bool) {
	switch t := err.(type) {
	case *StatusError:
		if t.Status().Details != nil {
			switch t.Status().Reason {
			case api.StatusReasonServerTimeout, api.StatusReasonTimeout:
				return t.Status().Details.RetryAfterSeconds, true
			}
		}
	}
	return 0, false
}

func reasonForError(err error) api.StatusReason {
	switch t := err.(type) {
	case *StatusError:
		return t.ErrStatus.Reason
	}
	return api.StatusReasonUnknown
}
