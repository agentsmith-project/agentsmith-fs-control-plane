package repoexec

import (
	"errors"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

const (
	jvsErrorCodeDetail = "jvs_error_code"
	jvsCommandDetail   = "jvs_command"
	jvsExitCodeDetail  = "jvs_exit_code"
)

func withJVSErrorDetails(details map[string]any, err error) map[string]any {
	var commandErr *jvsrunner.CommandError
	if !errors.As(err, &commandErr) {
		return details
	}
	out := asStringAnyMap(details)
	out[jvsErrorCodeDetail] = commandErr.Code
	out[jvsCommandDetail] = commandErr.Command
	out[jvsExitCodeDetail] = commandErr.ExitCode
	return out
}

func attachJVSErrorDetails(operation *operations.OperationRecord, details map[string]any) {
	if operation == nil || operation.Error == nil || details == nil {
		return
	}
	errorDetails := asStringAnyMap(operation.Error.Details)
	for _, key := range []string{jvsErrorCodeDetail, jvsCommandDetail, jvsExitCodeDetail} {
		if value, ok := details[key]; ok {
			errorDetails[key] = value
		}
	}
	operation.Error.Details = errorDetails
}

func isJVSRecoveryBlockingError(err error) bool {
	var commandErr *jvsrunner.CommandError
	return errors.As(err, &commandErr) && commandErr.Code == "E_RECOVERY_BLOCKING"
}
