# cluster-api-provider-mirage

A **fake** Cluster API provider that drives the *entire* CAPI cluster-creation
lifecycle to `Ready` **without creating any real infrastructure**. No VMs, no
cloud, no Docker containers, no real kubelets — infra clusters, "VMs", the
control plane, bootstrap data and Kubernetes `Node`s are all simulated. Every
simulated operation takes **real wall-clock time** (randomized, with jitter), so
there is an honest timeline to observe.

It exists to be an end-to-end **test target for controller-timeline tooling**
(e.g. "litecone"): deploy CAPI core + Mirage into a vanilla cluster, apply a
`Cluster` + `MachineDeployment`, and watch a dozen controllers interwork to carry
real `Cluster` / `Machine` / `MachineSet` / `MachineDeployment` /
`MachineHealthCheck` objects through their whole lifecycle.

Mirage is unusual in that it is an **infrastructure provider, a bootstrap
provider, and a control-plane provider at once** — one deployment, three API
groups — so the only other thing you need in the cluster is CAPI **core**.

Targets **Cluster API v1.14.x / the v1beta2 contract**.

---

## What is faked, and how

| Real provider would… | Mirage does… |
|---|---|
| Create a VPC / load balancer for the cluster | `MirageCluster` waits a randomized duration, then fabricates `spec.controlPlaneEndpoint` and reports `status.initialization.provisioned=true` |
| Boot a VM for each machine | `MirageMachine` waits a randomized "VM-create" duration, then sets `spec.providerID` and `status.initialization.provisioned=true` |
| Have a kubelet join the cluster as a Node | After a second randomized "node-register" delay, `MirageMachine` creates a real `Node` object (with real allocatable capacity) in the management cluster; CAPI core then resolves the Machine's `NodeRef` by matching `providerID` |
| Render cloud-init / ignition | `MirageConfig` waits, then writes a dummy bootstrap `Secret` (`value` key) and reports `status.initialization.dataSecretCreated=true` |
| Stand up etcd + apiserver, write a kubeconfig | `MirageControlPlane` publishes a `<cluster>-kubeconfig` **pointing back at the management cluster** (so `NodeRef`s resolve) and creates N control-plane `Machine`s |
| Run a kubelet that posts status and runs pods | A built-in **fake kubelet** renews each fake Node's lease/heartbeat and marks pods scheduled onto fake Nodes `Running` |

**The key trick for `NodeRef`s without a real workload cluster:** Mirage
registers its fake `Node`s *into the management cluster itself* and publishes a
per-cluster kubeconfig that points at that same management API server. So the
"workload cluster" is the management cluster. CAPI core's Machine controller
uses that kubeconfig, finds the fake Node by `providerID`, and sets the real
`NodeRef` — exactly as it would for a real provider. This same self-consistency
is what lets a real `cluster-autoscaler` and the real `kube-scheduler` close the
autoscaling loop (see below).

Fake worker Nodes carry a `mirage.cluster.x-k8s.io/fake:NoSchedule` taint so that
real system pods never land on a kubelet-less Node; workloads that *want* a
Mirage Node (the autoscaler demo) tolerate it.

### Timing (this is the point)

Nothing flips instantly. When an object enters an in-progress phase, the
controller records `provisioningStartedAt` and a randomized
`targetProvisioningSeconds`, then re-queues with `RequeueAfter`; it only
transitions once that wall-clock duration has elapsed. Two presets:

- `fast` (default) — seconds; for CI / e2e.
- `demo` — tens of seconds; lifelike, pleasant to watch.

Override globally with `--timing-profile=demo`, per-operation with flags
(`--machine-provision=40s` etc. — see `--help`), or **per object** with
annotations:

```yaml
metadata:
  annotations:
    mirage.cluster.x-k8s.io/provision-duration: "90s"
    mirage.cluster.x-k8s.io/provision-jitter: "30s"
    mirage.cluster.x-k8s.io/delete-duration: "45s"
```

This gives timeline oracles something real to assert on: phase durations,
ordering (bootstrap → infra → node → NodeRef), "settled within N seconds", and
no-flapping.

---

## The controllers and how they interwork

Ten controllers reconcile a single cluster — **five** from Mirage, **five** from
CAPI core:

Mirage (this repo):
1. **MirageCluster** (infra cluster) — fakes cluster infrastructure, sets the endpoint.
2. **MirageMachine** (infra machine) — fakes the VM, sets `providerID`, registers the fake Node.
3. **MirageConfig** (bootstrap) — fakes bootstrap data, writes the secret.
4. **MirageControlPlane** (control plane) — publishes the kubeconfig, creates/scales CP Machines.
5. **FakeKubelet** — keeps fake Nodes `Ready` and marks their pods `Running`.

CAPI core (installed by `clusterctl init`): **Cluster**, **Machine**,
**MachineSet**, **MachineDeployment**, **MachineHealthCheck**.

### Walking the create lifecycle

```
Cluster ─infrastructureRef─▶ MirageCluster      ─controlPlaneRef─▶ MirageControlPlane
MachineDeployment ─▶ MachineSet ─▶ Machine ─infrastructureRef─▶ MirageMachine
                                           └─bootstrap.configRef─▶ MirageConfig
```

1. You apply a `Cluster` (referencing `MirageCluster` + `MirageControlPlane`), a
   `MirageControlPlane`, templates, and a `MachineDeployment`.
2. **Cluster** controller sets owner refs on `MirageCluster` / `MirageControlPlane`.
   *(It resolves the `apiGroup/kind` refs via the `cluster.x-k8s.io/v1beta2`
   contract label on Mirage's CRDs — this must map to Mirage's CRD version
   `v1alpha1`.)*
3. **MirageCluster**: `Pending → Provisioning` (held ~randomized) `→ Provisioned`;
   sets the control-plane endpoint. Cluster `status.infrastructureReady=true`.
4. **MirageControlPlane** (gated on infra ready): publishes `<cluster>-kubeconfig`,
   then creates control-plane `Machine`s — each with a `MirageMachine` (from its
   `machineTemplate`) and a `MirageConfig`.
5. For every CP `Machine`: **MirageConfig** writes bootstrap data → **MirageMachine**
   fakes the VM (`providerID`) and registers a fake control-plane Node → **Machine**
   controller sets `NodeRef` → Machine `Running`. `MirageControlPlane` reports
   `controlPlaneInitialized`, then `ready` when all replicas are up. Cluster
   `status.controlPlaneReady=true`.
6. In parallel, **MachineDeployment → MachineSet** clone the worker templates into
   `MirageMachine` + `MirageConfig` per `Machine`; the same bootstrap → VM → node →
   `NodeRef` sequence runs. Workers reach `Running`.
7. **Cluster** phase → `Provisioned`, `Available=True`. Done — all faked.

### Scale & remediation

- **Scale** a `MachineDeployment` (`kubectl scale`): MachineSet creates/deletes
  `Machine`s; Mirage fake-provisions / fake-deprovisions (with the randomized
  latency) and registers / removes the fake Nodes.
- **MachineHealthCheck**: fault a worker Node
  (`kubectl annotate node <n> mirage.cluster.x-k8s.io/unhealthy=true`) — the fake
  kubelet reports it `NotReady` and stops its lease; MHC deletes the Machine; the
  MachineSet provisions a replacement.

---

## Install & run

Prereqs: a container runtime, `kind`, `kubectl`, `clusterctl` (v1.14+), `go`.

> ⚠️ Use a **throwaway** cluster. These steps create fake `Node`s and
> cluster-wide RBAC; do not run them against a cluster you care about.

```bash
# 1. Throwaway management cluster
make kind-up                       # kind create cluster --name mirage-dev

# 2. Cluster API CORE ONLY (+ cert-manager). No kubeadm providers — Mirage is its
#    own bootstrap + control-plane provider.
make capi-init
#   == CLUSTER_TOPOLOGY=true clusterctl init \
#        --core cluster-api:v1.14.4 --bootstrap "-" --control-plane "-"

# 3. Build + load + deploy Mirage (CRDs, RBAC, manager) into mirage-system
make dev                           # == kind-load deploy (+ rollout restart)

# 4. Flagship: a full faked cluster (3 CP + 3 workers)
make example-cluster
kubectl get cluster,miragecluster,miragecontrolplane,machinedeployment,machine -w
```

Everything settles to `Ready` in well under a minute on the `fast` profile:

```
NAME                                         AVAILABLE   PHASE         ...
cluster.cluster.x-k8s.io/mirage-quickstart   True        Provisioned
miragecontrolplane .../mirage-quickstart-cp  INITIALIZED=true READY=true REPLICAS=3 READYREPLICAS=3
machinedeployment  .../mirage-quickstart-md-0 AVAILABLE=True READY=3 ...
# 6 Machines Running, each with a NodeRef and providerID mirage://...
```

Deploy by hand instead of `make`: `kubectl apply -k config/default` (the image is
`ghcr.io/luthermonson/cluster-api-provider-mirage:dev`; override via the
`images:` block in `config/default/kustomization.yaml` or build locally with
`make docker-build`).

### clusterctl (optional)

`metadata.yaml` declares the v1beta2 contract so Mirage can be packaged as a
clusterctl provider. The simplest and most reproducible path, though, is the
`kubectl apply -k config/default` above, because Mirage serves three provider
types from one deployment.

---

## Scenarios

| Scenario | Manifest | Assert on |
|---|---|---|
| **Full create → Ready** | `examples/quickstart-cluster.yaml` | InfrastructureReady → ControlPlaneReady → Machines Running with NodeRefs → Cluster Provisioned |
| **Scale up / down** | `kubectl scale machinedeployment mirage-quickstart-md-0 --replicas=N` | Machines fake-provision/deprovision on the randomized timeline |
| **MHC remediation** | `examples/machinehealthcheck.yaml` + annotate a node unhealthy | faulted Machine deleted, replacement created |
| **Cluster autoscaling** (optional) | `examples/autoscaler/` | pending pods → autoscaler scales MD up → fake nodes register → pods bind & go Running; idle → scale down |

### Autoscaler loop (optional)

```bash
kubectl apply -f examples/autoscaler/01-autoscaling-machinedeployment.yaml
kubectl apply -f examples/autoscaler/03-cluster-autoscaler.yaml
kubectl apply -f examples/autoscaler/02-unschedulable-workload.yaml
kubectl get machinedeployment mirage-as -w      # scales 1 → up to 6
kubectl get pods -l app=hungry -o wide -w       # Pending → bound to new Mirage nodes → Running
```

cluster-autoscaler runs in `clusterapi` mode against the Mirage
`MachineDeployment`s. Because the fake Nodes advertise real allocatable CPU/mem/
pods, the real `kube-scheduler` actually binds the pending pods once Mirage
provisions new nodes; the fake kubelet then marks them `Running`. Scale-from-zero
and taint simulation work via `capacity.cluster-autoscaler.kubernetes.io/*`
annotations on the MachineDeployment. Delete the workload to watch scale-down
remove idle Mirage nodes. See `examples/autoscaler/` for details.

---

## E2E test

`test/e2e` drives create→Ready, scale up/down, and MHC remediation and asserts
each timeline. It runs against a live cluster that already has CAPI core + Mirage
deployed:

```bash
make kind-up capi-init dev     # one-time setup
make e2e                       # go test ./test/e2e/... -tags e2e -v -timeout 20m
```

---

## Repo layout

```
api/
  infrastructure/v1alpha1/   MirageCluster(+Template), MirageMachine(+Template)
  bootstrap/v1alpha1/        MirageConfig(+Template)
  controlplane/v1alpha1/     MirageControlPlane(+Template)
internal/
  controller/                the five Mirage reconcilers
  mirage/                    timing engine, fake node + fake kubelet, kubeconfig
cmd/                         manager entrypoint (flags, scheme, wiring)
config/                      kustomize: crd / rbac / manager / default
examples/                    quickstart cluster, MHC, autoscaler scenario
test/e2e/                    tagged e2e lifecycle test
```

---

## Research notes (what this was built against)

- **CAPI in-memory provider (CAPIM, `test/infrastructure/inmemory`)** fakes
  machines/nodes and runs a fake per-cluster API server for scale testing. It is
  the closest prior art. Mirage deliberately does **not** run a fake API server;
  instead it folds the "workload cluster" into the management cluster, which is
  far less code and makes the whole loop (NodeRefs, scheduler, autoscaler)
  self-consistent in one kind cluster. CAPIM is also infra-only (uses kubeadm
  bootstrap/control-plane); Mirage ships all three provider types so you only
  need CAPI core.
- **CAPD (cluster-api-provider-docker)** is the reference provider but uses real
  Docker containers + real kubeadm — not "no infrastructure".
- Mirage follows the published **v1beta2 provider contracts** for InfraCluster,
  InfraMachine, BootstrapConfig and ControlPlane.

## Open questions / caveats

- The fake "workload cluster" **is** the management cluster. Fake Nodes are real
  `Node` objects there (tainted, kubelet-less). This is deliberate and keeps the
  autoscaler/scheduler loop honest, but it does mean the management cluster's node
  list grows with the fake fleet. Per-cluster isolation would require a real fake
  API server (the CAPIM approach).
- The published kubeconfig authenticates as a `cluster-admin` ServiceAccount
  (`mirage-workload`) via a long-lived token Secret. Fine for a throwaway test
  cluster; not something to run anywhere real.
- Only the happy path and the one injected-fault path are modeled. Failure
  injection beyond "node unhealthy" (stuck provisioning, provision errors) would
  be natural next additions for a timeline oracle to assert on.
