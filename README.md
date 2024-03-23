# Election Agent
A high-performance and easy-use agent provides leader election service.

**For Developers:** *Simplify and Centralize Leader Election in Your System.*

Unburden your application code with a high-performance and user-friendly agent service for leader election. Eliminates complex implementations and leverages a reliable, centralized service for robust leader selection in your distributed systems.


**For System Administrators:** *Streamline Cluster Management with Efficient Leader Election.*

Manage your distributed systems with ease using an efficient agent service for leader election. Election Agent removes the need for intricate client-side implementations, providing a reliable and centralized leader selection mechanism for simplified cluster management.


**For End Users:** *Effortless Leader Election for Your Applications.*

Enjoy seamless leader election with a high-performance agent service. Forget about complex implementations – A reliable centralized service handles leader selection effortlessly in your distributed systems.

## Features
* Provides high-performance gRPC and HTTP Rest API service.
* Provides additional Kubernetes resource discovery API for retrieving the Pod, ReplicaSet, and Deployment relationship.
* Supports Kubernetes gRPC and HTTP liveness and readiness probe.

## How To Use
### Environment Variable
> Please refer to `internal/config.go` for details.

* *Application Settings*

  **EA_ENV** - The environment of the application.

  Defaults to `production`.

  Possible values: `production`, `development`, `test`.

  **EA_LOG_LEVEL** - The logging level.
  Defaults to `info`.
  Possible values: `debug`, `info`, `warning`, `error`, `panic`, `fatal`.

* *Kubernetes Settings*

  **EA_KUBE_ENABLE** - Whether to enable k8s service.

  Defaults to `true`

  **EA_KUBE_IN_CLUSTER** - InCluster indicates if the application is in or outside the k8s cluster.

  Defaults to `true`.

* *gRPC Settings*

  **EA_GRPC_ENABLE** - Whether to enable gRPC service.

  Defaults to `true`.

  **EA_GRPC_PORT** - The gRPC service port.

  Defaults to `443`.

* *HTTP Settings*

  **EA_HTTP_ENABLE** - Whether to enable HTTP service.

  Defaults to `true`.

  **EA_HTTP_PORT** - The HTTP service port.

  Defaults to `80`.

* *Redis Settings*

  **EA_REDIS_MODE** - The redis server mode.

  Different modes require different URL formats; please refer to the `EA_REDIS_URLS` for details.

  Defaults to `single`.

  Possible values: `single`,`cluster`,`failover`.

  **EA_REDIS_PREFIX** - The redis key prefix.
  The lease Redis key will be formatted as `[prefix]/[lease name]`.

  Defaults to `ela`.

  **EA_REDIS_URLS** - A URL list of redis server.

  Single mode: The URL format of each redis server is:
  `redis://<user>:<password>@<host>:<port>/<db_number>`.

  Failover mode: The URL format of each redis server is:
  `redis://<user>:<password>@<host>:<port>/<db_number>`.

  Cluster mode: The URL format of each redis server is:
  `redis://<user>:<password>@<host>:<port>?addr=<host2>:<port2>&addr=<host3>:<port3>`.

  **EA_REDIS_MASTER** - The redis master name.

  It only takes effect when the `mode` is `failover`

### gRPC API
Please refer to `proto/election_agent/v1/election_agent.proto` for details.

**Campaign**

`rpc Campaign(CampaignRequest) returns (CampaignResult)`

Campaign and election returns `CampaignResult.Elected=true` if successful; otherwise, `CampaignResult.Elected=false`.
The candidate needs to set a proper `CampaignRequest.Term` in milliseconds.

When a client campaigns an election successfully once, then it needs to call `ExtendElectedTerm` to extend the elected term for holding leadership.

**ExtendElectedTerm**
`rpc ExtendElectedTerm(ExtendElectedTermRequest) returns (BoolValue)`

Extends the elected term for holder leadership, returns `true` if successful, otherwise `flase`.

The elected leader needs to call this method at intervals to hold the leadership, or the election term will expire after the milliseconds of `CampaignRequest.Term`

**Resign**
`rpc Resign(ResignRequest) returns (BoolValue)`

Resign the election, it will revoke the `ResignRequest.Election` immediately.

**GetLeader**
`rpc GetLeader(GetLeaderRequest) returns (StringValue)`

Retrieve the leader name of the `GetLeaderRequest.Election`.

If no existing election is found, it returns an empty string and a `NotFound` gRPC status code.

**GetPods**
`rpc GetPods(GetPodsRequest) returns (Pods)`

Retrives a list of pods by `GetPodsRequest.Namespace` and `GetPodsRequest.Deployment`.

The pod item represents the relationship of `Pod`, `ReplicaSet` and `Deployment`. This information helps identify which pods are "newer" through `ReplicaSet.revision` during the k8s rolling update period.

The `PodStatus.terminating` helps identify whether a Pod is starting to enter the graceful shutdown phase.

> Note: The `PodStatus.phase` will be `Running` even `PodStatus.terminating` is true.

### HTTP API
Please refer to `api/openapi_v3.yaml` for details.

## Development Information
### Setup Development Environment
> This application requires Go v1.21+, and it recommends to use Go v1.22 for best performance.

Execute `make update-tools` to install/update development tools.
Or install the tools one by one:
```bash
# protobuf tools
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.32.0
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0
# mock tools
go install github.com/vektra/mockery/v2@v2.40.3
# linter
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.56.1
```

### Common & Useful Commands
> Please refer to `Makefile` for more commands.

* Execute `make run` to run agent.
* Execute `make build` to build `election-agent` binary.
* Execute `make test` to test all test cases.
* Execute `make lint` to lint codes.
* Execute `make proto-gen` to generate protobuf codes.
* Execute `make mock-gen` to generate mock codes.


## Deployment Information
### Kubernetes Notes
> Please refer to `examples/k8s` folder for k8s manifests.

To make the k8s resource discovery feature works in a k8s cluster. Remember to set `automountServiceAccountToken:true` in the Pod Spec for mounting service account token.
And it requires the following permission:

| apiGroups | resources                         | verbs     |
|-----------|-----------------------------------|-----------|
|           | pods                              | get, list |
| apps      | services. deployments,replicasets | get       |



The `ServiceAccount`, `Role` and `RoleBinding` manifest example:
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: election-agent
automountServiceAccountToken: true
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: election-agent
rules:
- apiGroups:
  - ""
  resources:
  - pods
  verbs:
  - get
  - list
- apiGroups:
  - "apps"
  resources:
  - services
  - deployments
  - replicasets
  verbs:
  - get
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: election-agent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: election-agent
subjects:
- kind: ServiceAccount
  name: election-agent
```


### Resource Allocation Recommendation
Test condition: 3000 concurrent clients, 3 Redis nodes.

Performance depends on the number of CPU cores, but performance improvement is not linearly related to CPU cores. The follow table lists the request-per-seconds by CPU cores.

> Allocate `1~2 vCPU` for a pod is a cost-effetive choice when deploying election agents with multiple replicas.

**go-redis verion**

| CPU Cores | RPS   | Perf. | DIff(prev). |
|-----------|-------|-------|-------------|
| 1         | 11535 | 100%  | 0%          |
| 2         | 19958 | 173%  | +73%        |
| 3         | 24844 | 215%  | +42%        |
| 4         | 26904 | 233%  | +18%        |
| 5         | 27337 | 236%  | +3%         |
| 6         | 27453 | 238%  | +2%         |

**rueidis verion**
| CPU Cores | RPS   | Perf. | DIff(prev). | Compare to go-redis |
|-----------|-------|-------|-------------|---------------------|
| 1         | 14001 | 100%  | 0%          | +21%                |
| 2         | 25552 | 182%  | +82%        | +28%                |
| 3         | 36598 | 261%  | +79%        | +47%                |
| 4         | 42735 | 305%  | +44%        | +59%                |
| 5         | 48741 | 348%  | +43%        | +78%                |
| 6         | 51254 | 366%  | +18         | +85%                |

For optimal performance, the following table lists recommended memory allocations in a pod for different numbers of concurrent clients.
| Clients | Recommended Memory |
|---------|--------------------|
| 1000    | 200 MiB            |
| 2000    | 400 MiB            |
| 3000    | 600 MiB            |
| 6000    | 1200 MiB           |


Example:
If the system expects to serve 6000 concurrent clients, and creates a service with 3 election agent replicas. It means one replica needs to serve ~2000 concurrent clients.
The recommended memory setting for each pod is 400 MiB

### Benchmarking
The `benchmark/` folder contains the corresponding `docker-compose.yaml` and grpc/http benchamrk program.

The sample usage:
```bash
# up services at first time
docker compose -f benchmark/docker-compose.yaml up -d
# or, start services next time
docker compose -f benchmark/docker-compose.yaml start

# run election agent
make run

# benchmarking gRPC service with 100 clients and 1000 iterations
go run benchmark/grpc/grpc_bench.go -c 100 -i 1000 -h localhost:8080

# benchmarking http service with 100 clients and 1000 iterations
go run benchmark/http/http_bench.go -c 100 -i 1000 -h localhost:8080

```
