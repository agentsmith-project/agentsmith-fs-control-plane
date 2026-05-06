package workerapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
)

type HTTPAuditDelivererConfig struct {
	Endpoint    string
	BearerToken string
	Timeout     time.Duration
	Client      *http.Client
}

type HTTPAuditDeliverer struct {
	endpoint    string
	bearerToken string
	client      *http.Client
}

func NewHTTPAuditDeliverer(config HTTPAuditDelivererConfig) (*HTTPAuditDeliverer, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		return nil, errors.New("audit delivery endpoint is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("audit delivery endpoint must be an http or https URL without userinfo")
	}
	if parsed.Scheme == "http" && !auditDeliveryEndpointHostIsLoopback(parsed.Hostname()) {
		return nil, errors.New("audit delivery endpoint must use https except for loopback http development endpoints")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		return nil, errors.New("audit delivery timeout must be positive")
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &HTTPAuditDeliverer{
		endpoint:    endpoint,
		bearerToken: strings.TrimSpace(config.BearerToken),
		client:      client,
	}, nil
}

func (deliverer *HTTPAuditDeliverer) DeliverAuditOutboxRecord(ctx context.Context, record audit.OutboxRecord) error {
	if deliverer == nil || deliverer.client == nil || strings.TrimSpace(deliverer.endpoint) == "" {
		return errors.New("audit delivery failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, deliverer.endpoint, bytes.NewReader(record.PayloadJSON))
	if err != nil {
		return errors.New("audit delivery failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-AFSCP-Audit-Event-Id", record.EventID)
	request.Header.Set("X-AFSCP-Audit-Event-Type", string(record.EventType))
	request.Header.Set("Idempotency-Key", record.EventID)
	if deliverer.bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+deliverer.bearerToken)
	}

	response, err := deliverer.client.Do(request)
	if err != nil {
		return errors.New("audit delivery failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("audit delivery failed: status %d", response.StatusCode)
	}
	return nil
}

func auditDeliveryEndpointHostIsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
