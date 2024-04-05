# Election Agent Metrics
## Configuration
* Set `EA_METRIC_ENABLE(metric.enable)` and `EA_HTTP_ENABLE(http.enable)` to `true`.
* Modify the list `EA_METRIC_REQUEST_DURATION_BUCKETS(metric.request_duration_buckets)` for specific buckets setting of `election_agent_request_duration_seconds` metric.


## Metrics

**Constant Labels (Applicable to all metrics):**

- `agent`: The agent name, mapping to configuration field: `EA_NAME(name)`.
- `zone`: The zone where the election agent resides, mapping to configuration field: `EA_ZONE_NAME(zone.name)`.

### election_agent_request_duration_seconds

- **Type:** Histogram
- **Labels:**
    - `action`: The action name. Possible values: `campaign`, `extend_elected_term`, `resign`, `handover`, `get_leader`.
    - `status`: The status of the action. Possible values: `success`, `fail`.
- **Purpose:** Tracks the distribution of latencies for election agent requests (in seconds).
- **Monitoring & Alerting:** Set latency thresholds and trigger alerts if exceeded consistently.

### election_agent_requests_in_flight

- **Type:** Gauge
- **Labels:**
    - `action`: The action name. Possible values: `campaign`, `extend_elected_term`, `resign`, `handover`, `get_leader`.
- **Purpose:** Indicates the current workload of an election agent. Valuable for autoscaling decisions.
- **Interpretation & Scaling:** Ideal value is zero. If `requests_in_flight` > `Number of Clients / 100`, consider scaling.

### election_agent_unavailable_total

- **Type:** Counter
- **Purpose:** Counts service unavailable errors, indicating potential agent disruptions.
- **Interpretation:** Spikes in this metric warrant investigation.

### election_agent_zone_coordinator_is_disconnected

- **Type:** Gauge
- **Value:** 0 (connected) or 1 (disconnected)
- **Purpose:** Immediate visibility into zone coordinator connectivity issues.
- **Alerting:** Triggers an immediate alert if the value becomes 1.

### election_agent_disconnected_peers

- **Type:** Gauge
- **Purpose:** Tracks the count of disconnected peers.
- **Alerting:** Triggers an alert if the value is greater than zero.

### election_agent_disconnected_redis_backends

- **Type:** Gauge
- **Purpose:** Tracks the count of disconnected Redis backends.
- **Alerting:** Triggers an immediate alert if the value is greater than zero.