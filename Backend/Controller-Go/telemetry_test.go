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

	extended := encodeHeartbeatTelemetryWithCounters(1234, 42, 3, 11, 2, 37.5, 8)
	_, measured, err := decodeHeartbeat(extended)
	if err != nil {
		t.Fatal(err)
	}
	if measured.CompletedTasks != 11 || measured.FailedTasks != 2 || !measured.MemoryAvailableKnown {
		t.Fatalf("unexpected extended telemetry: %#v", measured)
	}

	_, legacy, err := decodeHeartbeat(encodeHeartbeat(1234))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.CPUUtilizationPercent >= 0 || legacy.MemoryUtilizationPercent >= 0 {
		t.Fatalf("legacy heartbeat should leave unavailable metrics explicit: %#v", legacy)
	}
}
