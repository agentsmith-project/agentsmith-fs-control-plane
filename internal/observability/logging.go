package observability

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"strings"
)

const fallbackEventName = "afscp.event"

func NewJSONLogger(w io.Writer, opts *slog.HandlerOptions) *slog.Logger {
	return slog.New(NewJSONHandler(w, opts))
}

func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if w == nil {
		w = io.Discard
	}

	var options slog.HandlerOptions
	if opts != nil {
		options = *opts
	}

	replaceAttr := options.ReplaceAttr
	options.ReplaceAttr = func(groups []string, attr slog.Attr) slog.Attr {
		if replaceAttr != nil {
			attr = replaceAttr(groups, attr)
		}
		return redactLogAttr(attr)
	}

	return slog.NewJSONHandler(w, &options)
}

func LogEvent(ctx context.Context, logger *slog.Logger, level slog.Level, event string, message string, fields map[string]any) {
	if logger == nil {
		return
	}
	logger.LogAttrs(ctx, level, message, EventAttrs(event, fields)...)
}

func EventAttrs(event string, fields map[string]any) []slog.Attr {
	event = strings.TrimSpace(event)
	if event == "" {
		event = fallbackEventName
	}

	attrs := []slog.Attr{slog.String("event", event)}
	redacted := RedactFields(fields)
	if len(redacted) == 0 {
		return attrs
	}

	keys := make([]string, 0, len(redacted))
	for key := range redacted {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		attrs = append(attrs, slog.Any(key, redacted[key]))
	}
	return attrs
}

func redactLogAttr(attr slog.Attr) slog.Attr {
	if attr.Key == "" {
		return attr
	}
	if attr.Key == slog.MessageKey {
		attr.Key = "message"
	}
	if IsSensitiveField(attr.Key) {
		return slog.String(attr.Key, Redacted)
	}

	attr.Value = redactLogValue(attr.Key, attr.Value)
	return attr
}

func redactLogValue(key string, value slog.Value) slog.Value {
	value = value.Resolve()

	switch value.Kind() {
	case slog.KindString:
		return slog.AnyValue(redactValue(key, value.String()))
	case slog.KindGroup:
		group := value.Group()
		redacted := make([]slog.Attr, 0, len(group))
		for _, attr := range group {
			redacted = append(redacted, redactLogAttr(attr))
		}
		return slog.GroupValue(redacted...)
	case slog.KindAny:
		return slog.AnyValue(redactValue(key, value.Any()))
	default:
		return value
	}
}
