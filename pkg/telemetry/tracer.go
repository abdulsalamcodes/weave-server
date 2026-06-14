package telemetry

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	ServiceName    string
	ServiceVersion string
	OTLPEndpoint   string
	Environment    string
	Enabled        bool
}

func InitTracer(ctx context.Context, cfg Config, logger *slog.Logger) (trace.TracerProvider, func(), error) {
	if !cfg.Enabled {
		noop := trace.NewNoopTracerProvider()
		otel.SetTracerProvider(noop)
		return noop, func() {}, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", cfg.ServiceVersion),
			attribute.String("deployment.environment", cfg.Environment),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithInsecure()}
	if cfg.OTLPEndpoint != "" {
		opts = append(opts, otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint))
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
	)

	otel.SetTracerProvider(tp)

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			logger.Error("tracer provider shutdown", "error", err)
		}
	}

	logger.Info("otel tracer initialized",
		"endpoint", cfg.OTLPEndpoint,
		"service", cfg.ServiceName,
	)

	return tp, shutdown, nil
}
