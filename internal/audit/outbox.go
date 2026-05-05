package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidOutboxRequest    = errors.New("invalid audit outbox request")
	ErrInvalidOutboxRecord     = errors.New("invalid audit outbox record")
	ErrInvalidOutboxTransition = errors.New("invalid audit outbox transition")
)

type OutboxErrorCode string

const (
	OutboxErrorInvalidRequest    OutboxErrorCode = "AUDIT_OUTBOX_INVALID_REQUEST"
	OutboxErrorInvalidRecord     OutboxErrorCode = "AUDIT_OUTBOX_INVALID_RECORD"
	OutboxErrorInvalidTransition OutboxErrorCode = "AUDIT_OUTBOX_INVALID_TRANSITION"
)

type OutboxError struct {
	Code   OutboxErrorCode
	Field  string
	Reason string
	Cause  error
}

func (err *OutboxError) Error() string {
	if err == nil {
		return ""
	}
	if err.Field == "" {
		return fmt.Sprintf("%s: %s", err.Code, err.Reason)
	}
	return fmt.Sprintf("%s: %s: %s", err.Code, err.Field, err.Reason)
}

func (err *OutboxError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

type OutboxStatus string

const (
	OutboxStatusPending    OutboxStatus = "pending"
	OutboxStatusDelivering OutboxStatus = "delivering"
	OutboxStatusDelivered  OutboxStatus = "delivered"
	OutboxStatusRetryWait  OutboxStatus = "retry_wait"
	OutboxStatusFailed     OutboxStatus = "failed"
)

func (status OutboxStatus) String() string {
	return string(status)
}

func (status OutboxStatus) Valid() bool {
	switch status {
	case OutboxStatusPending,
		OutboxStatusDelivering,
		OutboxStatusDelivered,
		OutboxStatusRetryWait,
		OutboxStatusFailed:
		return true
	default:
		return false
	}
}

func (status OutboxStatus) Terminal() bool {
	switch status {
	case OutboxStatusDelivered, OutboxStatusFailed:
		return true
	default:
		return false
	}
}

type OutboxRecord struct {
	EventID         string
	EventType       EventType
	EventTime       time.Time
	PayloadJSON     json.RawMessage
	Status          OutboxStatus
	DeliveryAttempt int
	NextRetryAt     *time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeliveredAt     *time.Time
}

type DeliveryFailure struct {
	MaxAttempts int
	Backoff     time.Duration
	LastError   string
	Now         time.Time
}

func NewOutboxRecord(event Event, now time.Time) (OutboxRecord, error) {
	if strings.TrimSpace(event.EventID) == "" {
		return OutboxRecord{}, outboxInvalidRequest("event_id", "missing audit event id")
	}
	if event.Type == "" {
		return OutboxRecord{}, outboxInvalidRequest("event_type", "missing audit event type")
	}
	if event.Time.IsZero() {
		return OutboxRecord{}, outboxInvalidRequest("event_time", "missing audit event time")
	}
	if now.IsZero() {
		return OutboxRecord{}, outboxInvalidRequest("now", "outbox creation time must be set")
	}

	payload, err := json.Marshal(event.Sanitized())
	if err != nil {
		return OutboxRecord{}, outboxInvalidRequest("payload_json", "marshal sanitized audit event: "+err.Error())
	}

	record := OutboxRecord{
		EventID:         event.EventID,
		EventType:       event.Type,
		EventTime:       event.Time,
		PayloadJSON:     json.RawMessage(payload),
		Status:          OutboxStatusPending,
		DeliveryAttempt: 0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	return copyOutboxRecord(record), nil
}

func MarkDelivering(record OutboxRecord, owner string, now time.Time) (OutboxRecord, error) {
	if strings.TrimSpace(owner) == "" {
		return copyOutboxRecord(record), outboxInvalidRequest("owner", "missing delivery owner")
	}
	if now.IsZero() {
		return copyOutboxRecord(record), outboxInvalidRequest("now", "delivery transition time must be set")
	}
	if err := validateOutboxRecord(record); err != nil {
		return copyOutboxRecord(record), err
	}
	if record.Status != OutboxStatusPending && record.Status != OutboxStatusRetryWait {
		return copyOutboxRecord(record), outboxInvalidTransition("delivery_status", fmt.Sprintf("%q cannot transition to %q", record.Status, OutboxStatusDelivering))
	}

	updated := copyOutboxRecord(record)
	updated.Status = OutboxStatusDelivering
	updated.DeliveryAttempt++
	updated.NextRetryAt = nil
	updated.LastError = ""
	updated.UpdatedAt = now
	return updated, nil
}

func MarkDelivered(record OutboxRecord, now time.Time) (OutboxRecord, error) {
	if now.IsZero() {
		return copyOutboxRecord(record), outboxInvalidRequest("now", "delivery transition time must be set")
	}
	if err := validateOutboxRecord(record); err != nil {
		return copyOutboxRecord(record), err
	}
	if record.Status != OutboxStatusDelivering {
		return copyOutboxRecord(record), outboxInvalidTransition("delivery_status", fmt.Sprintf("%q cannot transition to %q", record.Status, OutboxStatusDelivered))
	}

	updated := copyOutboxRecord(record)
	deliveredAt := now
	updated.Status = OutboxStatusDelivered
	updated.NextRetryAt = nil
	updated.LastError = ""
	updated.DeliveredAt = &deliveredAt
	updated.UpdatedAt = now
	return updated, nil
}

func MarkDeliveryFailed(record OutboxRecord, failure DeliveryFailure) (OutboxRecord, error) {
	if failure.Now.IsZero() {
		return copyOutboxRecord(record), outboxInvalidRequest("now", "delivery transition time must be set")
	}
	if failure.MaxAttempts <= 0 {
		return copyOutboxRecord(record), outboxInvalidRequest("max_attempts", "max attempts must be positive")
	}
	if failure.Backoff < 0 {
		return copyOutboxRecord(record), outboxInvalidRequest("backoff", "retry backoff cannot be negative")
	}
	if err := validateOutboxRecord(record); err != nil {
		return copyOutboxRecord(record), err
	}
	if record.Status != OutboxStatusDelivering {
		return copyOutboxRecord(record), outboxInvalidTransition("delivery_status", fmt.Sprintf("%q cannot transition after delivery failure", record.Status))
	}

	updated := copyOutboxRecord(record)
	updated.LastError = RedactString(strings.TrimSpace(failure.LastError))
	if updated.LastError == "" {
		updated.LastError = "delivery failed"
	}
	updated.UpdatedAt = failure.Now
	updated.DeliveredAt = nil

	if updated.DeliveryAttempt >= failure.MaxAttempts {
		updated.Status = OutboxStatusFailed
		updated.NextRetryAt = nil
		return updated, nil
	}

	nextRetryAt := failure.Now.Add(failure.Backoff)
	updated.Status = OutboxStatusRetryWait
	updated.NextRetryAt = &nextRetryAt
	return updated, nil
}

func validateOutboxRecord(record OutboxRecord) error {
	if strings.TrimSpace(record.EventID) == "" {
		return outboxInvalidRecord("event_id", "missing audit event id")
	}
	if strings.TrimSpace(string(record.EventType)) == "" {
		return outboxInvalidRecord("event_type", "missing audit event type")
	}
	if record.EventTime.IsZero() {
		return outboxInvalidRecord("event_time", "missing audit event time")
	}
	if err := validateOutboxPayloadJSON(record.PayloadJSON); err != nil {
		return err
	}
	if !record.Status.Valid() {
		return outboxInvalidRecord("status", fmt.Sprintf("unknown delivery status %q", record.Status))
	}
	if record.DeliveryAttempt < 0 {
		return outboxInvalidRecord("delivery_attempt", "delivery attempt cannot be negative")
	}
	return nil
}

func validateOutboxPayloadJSON(payload json.RawMessage) error {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return outboxInvalidRecord("payload_json", "missing payload json")
	}

	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return outboxInvalidRecord("payload_json", "payload json must be a valid object")
	}
	if object == nil {
		return outboxInvalidRecord("payload_json", "payload json must be an object")
	}
	return nil
}

func copyOutboxRecord(record OutboxRecord) OutboxRecord {
	copied := record
	if record.PayloadJSON != nil {
		copied.PayloadJSON = append(json.RawMessage(nil), record.PayloadJSON...)
	}
	if record.NextRetryAt != nil {
		nextRetryAt := *record.NextRetryAt
		copied.NextRetryAt = &nextRetryAt
	}
	if record.DeliveredAt != nil {
		deliveredAt := *record.DeliveredAt
		copied.DeliveredAt = &deliveredAt
	}
	return copied
}

func outboxInvalidRequest(field, reason string) *OutboxError {
	return &OutboxError{
		Code:   OutboxErrorInvalidRequest,
		Field:  field,
		Reason: reason,
		Cause:  ErrInvalidOutboxRequest,
	}
}

func outboxInvalidRecord(field, reason string) *OutboxError {
	return &OutboxError{
		Code:   OutboxErrorInvalidRecord,
		Field:  field,
		Reason: reason,
		Cause:  ErrInvalidOutboxRecord,
	}
}

func outboxInvalidTransition(field, reason string) *OutboxError {
	return &OutboxError{
		Code:   OutboxErrorInvalidTransition,
		Field:  field,
		Reason: reason,
		Cause:  ErrInvalidOutboxTransition,
	}
}
