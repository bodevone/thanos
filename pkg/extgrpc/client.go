// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package extgrpc

import (
	"math"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware/v2"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/thanos-io/thanos/pkg/tls"
	"github.com/thanos-io/thanos/pkg/tracing"
)

// EndpointGroupGRPCOpts creates gRPC dial options for connecting to endpoint groups.
// For details on retry capabilities, see https://github.com/grpc/proposal/blob/master/A6-client-retries.md#retry-policy-capabilities
func EndpointGroupGRPCOpts() []grpc.DialOption {
	serviceConfig := `
{
  "loadBalancingPolicy":"round_robin",
  "retryPolicy": {
    "maxAttempts": 3,
    "initialBackoff": "0.1s",
    "backoffMultiplier": 2,
    "retryableStatusCodes": [
  	  "UNAVAILABLE"
    ]
  }
}`

	return []grpc.DialOption{
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 10 * time.Second, Timeout: 5 * time.Second}),
	}
}

// StoreClientGRPCOpts creates gRPC dial options for connecting to a store client.
func StoreClientGRPCOpts(logger log.Logger, reg prometheus.Registerer, tracer opentracing.Tracer, secure, skipVerify bool, cert, key, caCert, serverName string) ([]grpc.DialOption, error) {
	grpcMets := grpc_prometheus.NewClientMetrics()
	grpcMets.EnableClientHandlingTimeHistogram(
		grpc_prometheus.WithHistogramBuckets([]float64{0.001, 0.01, 0.1, 0.3, 0.6, 1, 3, 6, 9, 20, 30, 60, 90, 120, 240, 360, 720}),
	)
	dialOpts := []grpc.DialOption{
		// We want to make sure that we can receive huge gRPC messages from storeAPI.
		// On TCP level we can be fine, but the gRPC overhead for huge messages could be significant.
		// Current limit is ~2GB.
		// TODO(bplotka): Split sent chunks on store node per max 4MB chunks if needed.
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(math.MaxInt32)),
		grpc.WithUnaryInterceptor(
			grpc_middleware.ChainUnaryClient(
				grpcMets.UnaryClientInterceptor(),
				tracing.UnaryClientInterceptor(tracer),
			),
		),
		grpc.WithStreamInterceptor(
			grpc_middleware.ChainStreamClient(
				grpcMets.StreamClientInterceptor(),
				tracing.StreamClientInterceptor(tracer),
			),
		),
	}
	if reg != nil {
		reg.MustRegister(grpcMets)
	}

	if !secure {
		return append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials())), nil
	}

	level.Info(logger).Log("msg", "enabling client to server TLS")

	tlsCfg, err := tls.NewClientConfig(logger, cert, key, caCert, serverName, skipVerify)
	if err != nil {
		return nil, err
	}
	return append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg))), nil
}
