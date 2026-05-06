package workerapp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
)

func TestHTTPAuditDelivererPostsPayloadWithIdempotencyHeaders(t *testing.T) {
	var gotBody string
	var gotEventID string
	var gotEventType string
	var gotIdempotencyKey string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = string(body)
		gotEventID = r.Header.Get("X-AFSCP-Audit-Event-Id")
		gotEventType = r.Header.Get("X-AFSCP-Audit-Event-Type")
		gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		gotAuth = r.Header.Get("Authorization")
		if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", contentType)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	deliverer, err := NewHTTPAuditDeliverer(HTTPAuditDelivererConfig{
		Endpoint:    server.URL,
		BearerToken: "audit-secret-token",
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("NewHTTPAuditDeliverer: %v", err)
	}
	record := workerAppAuditRecord("audit-123", []byte(`{"audit_event_id":"audit-123","ok":true}`))

	if err := deliverer.DeliverAuditOutboxRecord(context.Background(), record); err != nil {
		t.Fatalf("DeliverAuditOutboxRecord: %v", err)
	}
	if gotBody != string(record.PayloadJSON) {
		t.Fatalf("body = %q, want payload %q", gotBody, string(record.PayloadJSON))
	}
	if gotEventID != "audit-123" || gotEventType != string(audit.EventTypeRepoCreate) || gotIdempotencyKey != "audit-123" {
		t.Fatalf("headers eventID/type/idempotency = %q/%q/%q", gotEventID, gotEventType, gotIdempotencyKey)
	}
	if gotAuth != "Bearer audit-secret-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestHTTPAuditDelivererFailureErrorsAreRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("token=server-secret"))
	}))
	defer server.Close()

	deliverer, err := NewHTTPAuditDeliverer(HTTPAuditDelivererConfig{
		Endpoint:    server.URL + "?token=endpoint-secret",
		BearerToken: "bearer-secret",
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("NewHTTPAuditDeliverer: %v", err)
	}

	err = deliverer.DeliverAuditOutboxRecord(context.Background(), workerAppAuditRecord("audit-fail", []byte(`{"ok":false}`)))
	if err == nil {
		t.Fatal("DeliverAuditOutboxRecord succeeded, want status failure")
	}
	lower := strings.ToLower(err.Error())
	for _, leaked := range []string{"endpoint-secret", "bearer-secret", "server-secret", server.URL} {
		if strings.Contains(lower, strings.ToLower(leaked)) {
			t.Fatalf("error leaked %q: %v", leaked, err)
		}
	}
	if !strings.Contains(lower, "audit delivery failed") {
		t.Fatalf("error = %q, want stable audit delivery failure", err)
	}
}

func TestHTTPAuditDelivererDoesNotFollowRedirects(t *testing.T) {
	var downstreamCalls int
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamCalls++
	}))
	defer downstream.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, downstream.URL+"/sink?token=redirect-secret", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	deliverer, err := NewHTTPAuditDeliverer(HTTPAuditDelivererConfig{
		Endpoint:    redirector.URL,
		BearerToken: "bearer-secret",
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("NewHTTPAuditDeliverer: %v", err)
	}

	err = deliverer.DeliverAuditOutboxRecord(context.Background(), workerAppAuditRecord("audit-redirect", []byte(`{"token":"body-secret"}`)))
	if err == nil {
		t.Fatal("DeliverAuditOutboxRecord succeeded, want redirect status failure")
	}
	if downstreamCalls != 0 {
		t.Fatalf("downstream calls = %d, want none", downstreamCalls)
	}
	lower := strings.ToLower(err.Error())
	for _, leaked := range []string{"redirect-secret", "bearer-secret", "body-secret", downstream.URL, redirector.URL} {
		if strings.Contains(lower, strings.ToLower(leaked)) {
			t.Fatalf("error leaked %q: %v", leaked, err)
		}
	}
	if !strings.Contains(lower, "audit delivery failed") {
		t.Fatalf("error = %q, want stable audit delivery failure", err)
	}
}

func TestHTTPAuditDelivererDoesNotFollowPermanentRedirects(t *testing.T) {
	var downstreamCalls int
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamCalls++
	}))
	defer downstream.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", downstream.URL+"/sink")
		w.WriteHeader(http.StatusPermanentRedirect)
	}))
	defer redirector.Close()

	deliverer, err := NewHTTPAuditDeliverer(HTTPAuditDelivererConfig{
		Endpoint: redirector.URL,
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewHTTPAuditDeliverer: %v", err)
	}

	err = deliverer.DeliverAuditOutboxRecord(context.Background(), workerAppAuditRecord("audit-redirect-308", []byte(`{"ok":true}`)))
	if err == nil {
		t.Fatal("DeliverAuditOutboxRecord succeeded, want redirect status failure")
	}
	if downstreamCalls != 0 {
		t.Fatalf("downstream calls = %d, want none", downstreamCalls)
	}
}

func TestHTTPAuditDelivererRejectsUnsafeEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "http non-loopback", endpoint: "http://audit.example/sink"},
		{name: "https userinfo", endpoint: "https://user:secret@audit.example/sink"},
		{name: "http userinfo", endpoint: "http://user:secret@127.0.0.1/sink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewHTTPAuditDeliverer(HTTPAuditDelivererConfig{
				Endpoint: tt.endpoint,
				Timeout:  time.Second,
			})
			if err == nil {
				t.Fatal("NewHTTPAuditDeliverer succeeded, want endpoint error")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "audit.example") {
				t.Fatalf("error leaked endpoint detail: %v", err)
			}
		})
	}
}

func TestHTTPAuditDelivererAllowsHTTPSAndLoopbackHTTP(t *testing.T) {
	for _, endpoint := range []string{
		"https://audit.example/sink",
		"http://localhost:8080/sink",
		"http://127.0.0.1:8080/sink",
		"http://[::1]:8080/sink",
	} {
		t.Run(endpoint, func(t *testing.T) {
			deliverer, err := NewHTTPAuditDeliverer(HTTPAuditDelivererConfig{
				Endpoint: endpoint,
				Timeout:  time.Second,
			})
			if err != nil {
				t.Fatalf("NewHTTPAuditDeliverer: %v", err)
			}
			if deliverer == nil {
				t.Fatal("deliverer nil, want configured deliverer")
			}
		})
	}
}
