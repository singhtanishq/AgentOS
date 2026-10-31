"""
AgentOS load test — k6 (Part 25.7).

Measures real throughput and latency against a running local stack with a
deterministic fake provider. Run:

    k6 run tests/load/api-load.js

Environment:
    BASE_URL   control plane base URL (default http://localhost:8080)
    API_KEY    an AgentOS API key with executions.submit + agents.read
    AGENT_ID   an active, deployed agent to execute

The test does not invent results: it reports measured HTTP throughput,
latency percentiles and failure rates. Baselines come from the local
development environment only and are not production claims.
"""

import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const API_KEY = __ENV.API_KEY || "";
const AGENT_ID = __ENV.AGENT_ID || "";

const submitFailures = new Rate("submit_failures");
const submitLatency = new Trend("submit_latency", true);

export const options = {
  scenarios: {
    steady_load: {
      executor: "ramping-vus",
      startVUs: 1,
      stages: [
        { duration: "30s", target: 5 },
        { duration: "1m", target: 5 },
        { duration: "30s", target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ["rate<0.05"],
    http_req_duration: ["p(95)<1500"],
    submit_failures: ["rate<0.05"],
  },
};

function headers() {
  return {
    "Content-Type": "application/json",
    Authorization: `Bearer ${API_KEY}`,
  };
}

export default function () {
  if (!API_KEY || !AGENT_ID) {
    sleep(1);
    return;
  }

  // Read path: agent registry.
  const listRes = http.get(`${BASE_URL}/v1/organizations/current/agents`, {
    headers: headers(),
  });
  check(listRes, { "agents list 2xx": (r) => r.status >= 200 && r.status < 300 });

  // Write path: execution submission (mock provider keeps the queue cheap).
  const submit = http.post(
    `${BASE_URL}/v1/organizations/current/executions`,
    JSON.stringify({
      agent_id: AGENT_ID,
      input: { query: `load test ${__VU}-${__ITER}` },
      idempotency_key: `k6-${__VU}-${__ITER}`,
    }),
    { headers: headers() },
  );
  submitLatency.add(submit.timings.duration);
  submitFailures.add(!(submit.status >= 200 && submit.status < 300));
  check(submit, {
    "submit 2xx/409": (r) =>
      (r.status >= 200 && r.status < 300) || r.status === 409,
  });

  sleep(1);
}
