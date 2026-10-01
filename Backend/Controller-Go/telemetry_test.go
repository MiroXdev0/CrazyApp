package main

import "testing"

func TestHeartbeatTelemetryRoundTrip(t *testing.T) {
	payload := encodeHeartbeatTelemetry(1234, 42, 3, 37.5, 8)
	timestamp, telemetry, err := decodeHeartbeat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if timestamp != 1234 || telemetry.UptimeSeconds != 42 || telemetry.ActiveTasks != 3 {
		t.Fatalf("unexpected heartbeat telemetry: %#v", telemetry)
	}
	if telemetry.CPUUtilizationPercent != 37.5 || telemetry.MemoryAvailableGB != 8 {
		t.Fatalf("unexpected measurements: %#v", telemetry)
	}

	_, legacy, err := decodeHeartbeat(encodeHeartbeat(1234))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.CPUUtilizationPercent >= 0 || legacy.MemoryUtilizationPercent >= 0 {
		t.Fatalf("legacy heartbeat should leave unavailable metrics explicit: %#v", legacy)
	}
}
