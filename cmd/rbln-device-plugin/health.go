/*
 * Copyright 2025 The Kubernetes Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/*
Modifications Copyright 2026 Rebellions Inc.

This file is derived from github.com/kubernetes-sigs/dra-example-driver
(cmd/dra-example-kubeletplugin/health.go, commit 7c9b507c) and has been
modified by Rebellions Inc. See NOTICE for details.
*/

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type healthServer struct {
	grpc_health_v1.UnimplementedHealthServer

	server   *grpc.Server
	listener net.Listener
	wg       sync.WaitGroup
	serving  atomic.Bool
}

func startHealthcheck(port int) (*healthServer, error) {
	if port < 0 {
		return nil, nil
	}

	addr := net.JoinHostPort("", strconv.Itoa(port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for healthcheck service at %s: %w", addr, err)
	}

	server := grpc.NewServer()
	health := &healthServer{
		server:   server,
		listener: listener,
	}
	grpc_health_v1.RegisterHealthServer(server, health)

	health.wg.Add(1)
	go func() {
		defer health.wg.Done()
		slog.Info("Started healthcheck service", "addr", listener.Addr().String())
		if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			slog.Error("Healthcheck service terminated", "err", err)
		}
	}()

	return health, nil
}

func (h *healthServer) SetServing(serving bool) {
	h.serving.Store(serving)
}

func (h *healthServer) Stop() {
	if h.server != nil {
		h.server.GracefulStop()
	}
	if h.listener != nil {
		_ = h.listener.Close()
	}
	h.wg.Wait()
}

func (h *healthServer) Check(_ context.Context, request *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	switch request.GetService() {
	case "", "liveness":
	default:
		return nil, status.Error(codes.NotFound, "unknown service")
	}

	response := &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
	}
	if h.serving.Load() {
		response.Status = grpc_health_v1.HealthCheckResponse_SERVING
	}

	return response, nil
}
