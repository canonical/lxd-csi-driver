package errors

import (
	"context"
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/canonical/lxd/shared/api"
)

// ToGRPCCode maps the given error to a gRPC error code.
// It recognizes both standard Go errors as well as LXD API errors.
// If the error is not recognized, an internal error is returned.
func ToGRPCCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}

	switch {
	case api.StatusErrorCheck(err, http.StatusBadRequest): // 400
		return codes.InvalidArgument
	case api.StatusErrorCheck(err, http.StatusUnauthorized): // 401
		return codes.Unauthenticated
	case api.StatusErrorCheck(err, http.StatusForbidden): // 403
		return codes.PermissionDenied
	case api.StatusErrorCheck(err, http.StatusNotFound): // 404
		return codes.NotFound
	case api.StatusErrorCheck(err, http.StatusConflict): // 409
		return codes.AlreadyExists
	case api.StatusErrorCheck(err, http.StatusPreconditionFailed): // 412
		return codes.FailedPrecondition
	case errors.Is(err, context.DeadlineExceeded):
		return codes.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		return codes.Canceled
	}

	return codes.Internal
}
