package operations

type RepoJVSMutationGateStatus struct {
	InProgress       bool
	OperationID      string
	OperationType    OperationType
	OperationState   OperationState
	RecoveryRequired bool
}

func NewRepoJVSMutationGateStatus(operationID string, operationType OperationType, operationState OperationState) RepoJVSMutationGateStatus {
	status := RepoJVSMutationGateStatus{
		InProgress:     operationID != "",
		OperationID:    operationID,
		OperationType:  operationType,
		OperationState: operationState,
	}
	status.RecoveryRequired = operationState == OperationStateOperatorInterventionRequired
	return status
}
