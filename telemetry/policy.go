package telemetry

// Authorize is this service's inbound policy. Peer authentication is supplied by the SDK.
func Authorize(caller, method, path string) bool {
	if path == "/readyz" || path == "/metrics" {
		return method == "GET" && (caller == "telemetry" || caller == "control")
	}

	return false
}
