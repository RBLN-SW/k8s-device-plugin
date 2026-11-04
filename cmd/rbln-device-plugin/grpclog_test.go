/*
Copyright 2026 Rebellions Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"log/slog"
	"testing"
)

// gRPC's info chatter must not reach the default gate, while its warnings and
// errors must — they are how transport failures become visible.
func TestGRPCLoggerMapsSeveritiesAndTagsComponent(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)

	logger := grpcLogger{}
	logger.Info("connection bookkeeping")
	logger.Warningf("half %s", "closed")
	logger.Errorln("transport failed")

	records := decodeRecords(t, buf)
	if len(records) != 2 {
		t.Fatalf("expected 2 records past the info gate, got %d: %s", len(records), buf.String())
	}
	if records[0]["level"] != "WARN" || records[0]["msg"] != "half closed" {
		t.Fatalf("warning record = %v", records[0])
	}
	// Errorln's trailing newline must not survive into the message.
	if records[1]["level"] != "ERROR" || records[1]["msg"] != "transport failed" {
		t.Fatalf("error record = %v", records[1])
	}
	for _, record := range records {
		if record["component"] != "grpc" {
			t.Fatalf("record is not attributed to grpc: %v", record)
		}
	}
}

func TestGRPCLoggerVerbosityFollowsDebugGate(t *testing.T) {
	logger := grpcLogger{}

	captureLogs(t, slog.LevelInfo)
	if logger.V(0) {
		t.Fatal("gRPC verbose logging must be off at the info gate")
	}

	buf := captureLogs(t, slog.LevelDebug)
	if !logger.V(0) {
		t.Fatal("gRPC verbose logging must follow the debug gate")
	}

	logger.Info("connection bookkeeping")
	records := decodeRecords(t, buf)
	if len(records) != 1 || records[0]["level"] != "DEBUG" {
		t.Fatalf("info records must land at debug: %s", buf.String())
	}
}
